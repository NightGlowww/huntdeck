package browser

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeVNCProc struct {
	done    chan struct{}
	stopped atomic.Bool
	once    sync.Once
}

func (p *fakeVNCProc) Stop() {
	p.once.Do(func() {
		p.stopped.Store(true)
		close(p.done)
	})
}

func (p *fakeVNCProc) Done() <-chan struct{} { return p.done }

func TestOnDemandVNC(t *testing.T) {
	t.Run("no active display returns ErrNoDisplay without launching", func(t *testing.T) {
		var launched int
		v := &OnDemandVNC{
			addr:      "127.0.0.1:5900",
			displayFn: func() string { return "" },
			idleDelay: 20 * time.Millisecond,
			launch: func(context.Context, string) (vncProc, error) {
				launched++
				return &fakeVNCProc{done: make(chan struct{})}, nil
			},
		}
		_, _, err := v.Attach(context.Background())
		if !errors.Is(err, ErrNoDisplay) {
			t.Fatalf("Attach() err = %v, want ErrNoDisplay", err)
		}
		if launched != 0 {
			t.Fatalf("launched = %d, want 0", launched)
		}
	})

	t.Run("reuses across concurrent viewers and stops after idle delay", func(t *testing.T) {
		disp := ":99"
		var launched []string
		var procs []*fakeVNCProc
		v := &OnDemandVNC{
			addr:      "127.0.0.1:5900",
			displayFn: func() string { return disp },
			idleDelay: 30 * time.Millisecond,
			launch: func(_ context.Context, d string) (vncProc, error) {
				launched = append(launched, d)
				p := &fakeVNCProc{done: make(chan struct{})}
				procs = append(procs, p)
				return p, nil
			},
		}

		addr1, rel1, err := v.Attach(context.Background())
		if err != nil || addr1 != "127.0.0.1:5900" {
			t.Fatalf("Attach 1 = (%q, %v)", addr1, err)
		}
		addr2, rel2, err := v.Attach(context.Background())
		if err != nil || addr2 != "127.0.0.1:5900" {
			t.Fatalf("Attach 2 = (%q, %v)", addr2, err)
		}
		if len(launched) != 1 {
			t.Fatalf("launched = %v, want 1 launch shared by 2 viewers", launched)
		}

		// Idempotent release of viewer 1 keeps server running for viewer 2.
		rel1()
		rel1()
		time.Sleep(50 * time.Millisecond)
		if procs[0].stopped.Load() {
			t.Fatal("stopped while second viewer still attached")
		}

		// Display switches (e.g. Chrome restarted on :100): old stopped, new started.
		disp = ":100"
		_, rel3, err := v.Attach(context.Background())
		if err != nil {
			t.Fatalf("Attach 3 = %v", err)
		}
		if !procs[0].stopped.Load() || len(launched) != 2 || launched[1] != ":100" {
			t.Fatalf("expected restart on display switch; launched=%v stopped0=%v", launched, procs[0].stopped.Load())
		}

		// Releasing remaining viewers triggers idle stop.
		rel2()
		rel3()
		deadline := time.Now().Add(time.Second)
		for !procs[1].stopped.Load() && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if !procs[1].stopped.Load() {
			t.Fatal("expected idle timer to stop second x11vnc process")
		}
		v.Close()
	})
}
