package browser

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Shared is a virtual display kept for the whole run, with a VNC server on
// it, so the login browsers can be watched and taken over (through noVNC)
// when a login needs a person: a CAPTCHA, a new-device check. Browsers use
// it through Options.Display.
type Shared struct {
	Display string // the X display, ":99"
	VNCAddr string // the VNC server, on the loopback interface only
	xvfb    *exec.Cmd
	vnc     *exec.Cmd
}

// StartShared starts the display and a VNC server for it on 127.0.0.1:port.
// The server has no password: only HuntDeck, on the same machine, reaches it.
func StartShared(ctx context.Context, port int) (*Shared, error) {
	path, err := exec.LookPath("x11vnc")
	if err != nil {
		return nil, errors.New("browser: no x11vnc to show the display (install it, or turn VNC off)")
	}
	xvfb, display, err := startXvfb(ctx)
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	//nolint:gosec,noctx // a fixed program and arguments; it outlives ctx, like the display
	vnc := exec.Command(path, "-display", display, "-localhost", "-rfbport", strconv.Itoa(port),
		"-forever", "-shared", "-nopw", "-quiet", "-noxdamage")
	if err := vnc.Start(); err != nil {
		stopXvfb(xvfb)
		return nil, fmt.Errorf("browser: start x11vnc: %w", err)
	}
	s := &Shared{Display: display, VNCAddr: addr, xvfb: xvfb, vnc: vnc}
	// Wait until it listens, so the first viewer doesn't find it closed.
	deadline := time.Now().Add(15 * time.Second)
	for {
		d := net.Dialer{Timeout: time.Second}
		c, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = c.Close()
			return s, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			s.Close()
			return nil, fmt.Errorf("browser: x11vnc didn't listen on %s", addr)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Close stops the VNC server and the display.
func (s *Shared) Close() {
	if s.vnc != nil && s.vnc.Process != nil {
		_ = s.vnc.Process.Kill()
		_ = s.vnc.Wait()
	}
	stopXvfb(s.xvfb)
}

// ErrNoDisplay is returned by OnDemandVNC.Attach when no login browser
// display is active.
var ErrNoDisplay = errors.New("browser: login browser is not running on a display")

const defaultVNCIdle = 15 * time.Second

type vncProc interface {
	Stop()
	Done() <-chan struct{}
}

type osVNCProc struct {
	cmd  *exec.Cmd
	done chan struct{}
	once sync.Once
}

func (p *osVNCProc) Stop() {
	p.once.Do(func() {
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		<-p.done
	})
}

func (p *osVNCProc) Done() <-chan struct{} { return p.done }

// OnDemandVNC runs x11vnc only while someone is watching the running login
// browser in Settings › Browser, attaching to whatever display Chrome owns
// and shutting down shortly after the last viewer disconnects.
type OnDemandVNC struct {
	addr      string
	displayFn func() string
	idleDelay time.Duration
	launch    func(ctx context.Context, display string) (vncProc, error)

	startMu sync.Mutex
	mu      sync.Mutex
	proc    vncProc
	procDis string
	viewers int
	timer   *time.Timer
	closed  bool
}

// NewOnDemandVNC returns a controller that attaches x11vnc on 127.0.0.1:port
// to the active display returned by displayFn whenever a viewer connects.
func NewOnDemandVNC(port int, displayFn func() string) (*OnDemandVNC, error) {
	path, err := exec.LookPath("x11vnc")
	if err != nil {
		return nil, errors.New("browser: no x11vnc to show the display")
	}
	if displayFn == nil {
		return nil, errors.New("browser: nil display provider")
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	return &OnDemandVNC{
		addr:      addr,
		displayFn: displayFn,
		idleDelay: defaultVNCIdle,
		launch: func(ctx context.Context, display string) (vncProc, error) {
			return startX11VNC(ctx, path, display, port, addr)
		},
	}, nil
}

type tailBuf struct {
	mu  sync.Mutex
	buf []byte
}

func (b *tailBuf) Write(p []byte) (int, error) {
	const max = 1024
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > max {
		b.buf = b.buf[len(b.buf)-max:]
	}
	return len(p), nil
}

func (b *tailBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.buf))
}

func startX11VNC(ctx context.Context, path, display string, port int, addr string) (vncProc, error) {
	//nolint:gosec,noctx // x11vnc outlives the connect ctx until all viewers leave
	cmd := exec.Command(path, "-display", display, "-localhost", "-rfbport", strconv.Itoa(port),
		"-forever", "-shared", "-nopw", "-quiet", "-noxdamage")
	var stderr tailBuf
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("browser: start x11vnc: %w", err)
	}
	p := &osVNCProc{cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		d := net.Dialer{Timeout: time.Second}
		c, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = c.Close()
			return p, nil
		}
		select {
		case <-p.done:
			if msg := stderr.String(); msg != "" {
				return nil, fmt.Errorf("browser: x11vnc exited before listening on %s: %s", addr, msg)
			}
			return nil, fmt.Errorf("browser: x11vnc exited before listening on %s", addr)
		default:
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			p.Stop()
			if msg := stderr.String(); msg != "" {
				return nil, fmt.Errorf("browser: x11vnc didn't listen on %s: %s", addr, msg)
			}
			return nil, fmt.Errorf("browser: x11vnc didn't listen on %s", addr)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// Attach ensures x11vnc is serving the current login browser display and
// returns its loopback address plus an idempotent release callback.
func (v *OnDemandVNC) Attach(ctx context.Context) (string, func(), error) {
	v.startMu.Lock()
	defer v.startMu.Unlock()

	disp := v.displayFn()
	if disp == "" {
		return "", nil, ErrNoDisplay
	}

	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return "", nil, errors.New("browser: VNC controller closed")
	}
	if v.timer != nil {
		v.timer.Stop()
		v.timer = nil
	}
	if v.proc != nil {
		select {
		case <-v.proc.Done():
			v.proc = nil
		default:
			if v.procDis != disp {
				old := v.proc
				v.proc = nil
				v.mu.Unlock()
				old.Stop()
				v.mu.Lock()
			}
		}
	}
	needStart := v.proc == nil
	v.mu.Unlock()

	if needStart {
		p, err := v.launch(ctx, disp)
		if err != nil {
			return "", nil, err
		}
		v.mu.Lock()
		if v.closed {
			v.mu.Unlock()
			p.Stop()
			return "", nil, errors.New("browser: VNC controller closed")
		}
		v.proc = p
		v.procDis = disp
	} else {
		v.mu.Lock()
	}
	v.viewers++
	v.mu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(v.release)
	}
	return v.addr, release, nil
}

func (v *OnDemandVNC) release() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.viewers > 0 {
		v.viewers--
	}
	if v.closed || v.viewers > 0 || v.proc == nil {
		return
	}
	delay := v.idleDelay
	procRef := v.proc
	v.timer = time.AfterFunc(delay, func() {
		v.mu.Lock()
		if v.closed || v.viewers > 0 || v.proc != procRef {
			v.mu.Unlock()
			return
		}
		v.proc = nil
		v.timer = nil
		v.mu.Unlock()
		procRef.Stop()
	})
}

// Close stops any active x11vnc server and idle timer.
func (v *OnDemandVNC) Close() {
	v.mu.Lock()
	v.closed = true
	if v.timer != nil {
		v.timer.Stop()
		v.timer = nil
	}
	p := v.proc
	v.proc = nil
	v.mu.Unlock()
	if p != nil {
		p.Stop()
	}
}
