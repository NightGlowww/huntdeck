// Package browser drives a headless Chrome or Chromium through the Chrome
// DevTools Protocol, for the few platform logins that need a real browser
// (Intigriti's login page scores visitors with reCAPTCHA). It speaks CDP
// over --remote-debugging-pipe, so it needs no network port and no
// third-party modules, and it covers only what logins use: open a page,
// run JavaScript, type, and read cookies.
//
// Each Browser is a separate Chrome process, with a throwaway profile that
// Close deletes, or a kept one (Options.Profile).
package browser

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// Find returns the Chrome executable to use: path if set, else the first
// candidate on PATH.
func Find(path string) (string, error) {
	if path != "" {
		return exec.LookPath(path)
	}
	for _, c := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no Chrome or Chromium found (install one, or set its path)")
}

// Options configures Start.
type Options struct {
	Path string // the executable (see Find)
	// Headless runs Chrome without a window. By default it runs as a normal,
	// windowed Chrome, which login pages trust more: on the display in
	// $DISPLAY, or without one on a virtual display (Xvfb, which must be
	// installed) started for it.
	Headless bool
	// Proxy, if set, is an HTTP proxy (http://host:port) for all of
	// Chrome's traffic. Chrome won't take a username and password on its
	// command line, so if the URL has them, Start answers the proxy's
	// requests for them itself.
	Proxy *url.URL
	// TimeZone is Chrome's clock (an IANA name); match it to the proxy's
	// country, as a person there would have it. Empty: the system's.
	TimeZone string
	// Profile, if set, is a Chrome profile directory kept between runs, so
	// the browser has a history like a person's rather than starting empty
	// every time. Only one Browser may use it at a time.
	Profile string
	// Display, if set, is the X display a windowed Chrome shows on, such
	// as a Shared one that can be watched; otherwise $DISPLAY, or a virtual
	// display of its own.
	Display string
	// URL is the page it opens at; by default a blank one.
	URL string
	// Logf, if set, receives Chrome's own stderr lines, for debugging.
	Logf func(format string, args ...any)
}

// Browser is a running Chrome.
type Browser struct {
	cmd     *exec.Cmd
	xvfb    *exec.Cmd // the virtual display, if Start started one
	display string
	profile string
	temp    bool           // the profile is Start's own, deleted by Close
	in      io.WriteCloser // commands to Chrome
	waitErr chan error
	exited  chan struct{} // closed when Chrome has exited

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan reply
	closed  bool
	readErr error

	proxyAuth *url.Userinfo // answers the proxy's requests for credentials
}

var versionRE = regexp.MustCompile(`(\d+)\.\d+\.\d+\.\d+`)

// userAgent is a normal desktop Chrome user agent for the installed
// version. Headless Chrome otherwise announces itself as "HeadlessChrome",
// which login pages turn away.
func userAgent(ctx context.Context, path string) string {
	out, _ := exec.CommandContext(ctx, path, "--version").Output()
	v := versionRE.FindString(string(out))
	if v == "" {
		v = "141.0.0.0"
	}
	return "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + v + " Safari/537.36"
}

// Start launches Chrome. ctx bounds only the launch; call Close when done.
func Start(ctx context.Context, o Options) (*Browser, error) {
	if o.Proxy != nil && (o.Proxy.Scheme != "http" || o.Proxy.Host == "") {
		return nil, errors.New("browser: the proxy must be http://[user:password@]host:port")
	}
	profile, temp := o.Profile, o.Profile == ""
	if temp {
		var err error
		if profile, err = os.MkdirTemp("", "huntdeck-chrome-"); err != nil {
			return nil, fmt.Errorf("browser: profile dir: %w", err)
		}
	} else {
		if err := os.MkdirAll(profile, 0o700); err != nil {
			return nil, fmt.Errorf("browser: profile dir: %w", err)
		}
		// A Chrome that was killed leaves its lock behind, naming a host
		// (a container's changes every run) that Chrome would then think
		// still has the profile open.
		for _, f := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
			_ = os.Remove(filepath.Join(profile, f))
		}
	}
	removeProfile := func() {
		if temp {
			_ = os.RemoveAll(profile)
		}
	}
	// Chrome reads commands from fd 3 and writes replies to fd 4.
	cmdR, cmdW, err := os.Pipe()
	if err != nil {
		removeProfile()
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		removeProfile()
		_, _ = cmdR.Close(), cmdW.Close()
		return nil, err
	}
	args := []string{
		"--remote-debugging-pipe",
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-blink-features=AutomationControlled",
		"--disable-dev-shm-usage",
		"--lang=en-US",
		// WebRTC would otherwise reach out directly, around the proxy, and
		// show the page the machine's own address.
		"--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
	}
	env := os.Environ()
	if o.TimeZone != "" {
		env = append(env, "TZ="+o.TimeZone)
	}
	var xvfb *exec.Cmd
	var display string
	if o.Headless {
		args = append(args, "--headless=new", "--window-size=1400,900", "--user-agent="+userAgent(ctx, o.Path))
	} else {
		// A windowed Chrome as a person runs it: its own user agent, a
		// full-size window, and WebGL from Chrome's software renderer.
		args = append(args, "--window-position=0,0", "--window-size=1920,1080", // the whole display
			"--use-gl=angle", "--use-angle=swiftshader", "--enable-unsafe-swiftshader")
		switch {
		case o.Display != "":
			display = o.Display
			env = append(env, "DISPLAY="+display)
		case os.Getenv("DISPLAY") == "":
			if xvfb, display, err = startXvfb(ctx); err != nil {
				removeProfile()
				_, _, _, _ = cmdR.Close(), cmdW.Close(), outR.Close(), outW.Close()
				return nil, err
			}
			env = append(env, "DISPLAY="+display)
		default:
			display = os.Getenv("DISPLAY")
		}
	}
	// Driving a browser over CDP makes it announce itself: navigator.webdriver
	// is true and a page that scores visitors reads that first. Measured: with a
	// residential Japanese exit and the flag absent, Intigriti still answered
	// "Bot-like behavior detected" after the password. This is the login browser
	// of the person who configured it, on their own account.
	args = append(args, "--disable-blink-features=AutomationControlled")
	if o.Proxy != nil {
		args = append(args, "--proxy-server=http://"+o.Proxy.Host)
	}
	args = append(args, cmp.Or(o.URL, "about:blank"))
	if os.Geteuid() == 0 || os.Getenv("container") != "" || fileExists("/.dockerenv") {
		// Chrome's sandbox needs privileges a container or root doesn't give it.
		args = append(args, "--no-sandbox")
	}
	// Not CommandContext: Chrome outlives ctx, which only bounds the launch.
	cmd := exec.Command(o.Path, args...) //nolint:gosec,noctx // the path is the configured browser; see above
	cmd.ExtraFiles = []*os.File{cmdR, outW}
	cmd.Env = env
	var stderr io.ReadCloser
	if o.Logf != nil {
		if stderr, err = cmd.StderrPipe(); err != nil {
			return nil, err
		}
	}
	if err := cmd.Start(); err != nil {
		removeProfile()
		_, _, _, _ = cmdR.Close(), cmdW.Close(), outR.Close(), outW.Close()
		stopXvfb(xvfb)
		return nil, fmt.Errorf("browser: start %s: %w", o.Path, err)
	}
	_, _ = cmdR.Close(), outW.Close() // Chrome's ends
	b := &Browser{cmd: cmd, xvfb: xvfb, display: display, profile: profile, temp: temp, in: cmdW, pending: map[int64]chan reply{}, waitErr: make(chan error, 1), exited: make(chan struct{})}
	if o.Proxy != nil && o.Proxy.User != nil {
		b.proxyAuth = o.Proxy.User
	}
	if stderr != nil {
		go func() {
			sc := bufio.NewScanner(stderr)
			for sc.Scan() {
				o.Logf("chrome: %s", sc.Text())
			}
		}()
	}
	go b.read(outR)
	go func() {
		err := cmd.Wait()
		close(b.exited)
		b.waitErr <- err
	}()
	// The first command answers once Chrome is ready.
	if _, err := b.call(ctx, "", "Browser.getVersion", nil); err != nil {
		_ = b.Close()
		return nil, fmt.Errorf("browser: Chrome didn't start: %w", err)
	}
	if b.proxyAuth != nil {
		// Pause every request so the proxy's credential challenges reach us
		// (see event); the requests themselves go on unchanged.
		if _, err := b.call(ctx, "", "Fetch.enable", map[string]any{
			"handleAuthRequests": true, "patterns": []map[string]any{{"urlPattern": "*"}},
		}); err != nil {
			_ = b.Close()
			return nil, fmt.Errorf("browser: proxy login: %w", err)
		}
	}
	return b, nil
}

// Exited is closed once Chrome has exited, by Close or otherwise (its
// last window closed by whoever is using it).
func (b *Browser) Exited() <-chan struct{} { return b.exited }

// Display returns the X display Chrome was launched on, or "" when headless.
func (b *Browser) Display() string { return b.display }

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Close ends Chrome and deletes its profile, unless it's a kept one.
func (b *Browser) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()
	msg, _ := json.Marshal(message{ID: 1 << 40, Method: "Browser.close"})
	_, _ = b.in.Write(append(msg, 0))
	_ = b.in.Close()
	// Chrome's exit status says nothing useful here (a kill is an error).
	var exitErr error
	select {
	case exitErr = <-b.waitErr:
	case <-time.After(5 * time.Second):
		_ = b.cmd.Process.Kill()
		exitErr = <-b.waitErr
	}
	_ = exitErr
	stopXvfb(b.xvfb)
	if !b.temp {
		return nil
	}
	return os.RemoveAll(b.profile)
}
