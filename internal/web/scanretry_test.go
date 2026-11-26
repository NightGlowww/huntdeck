package web

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/store"
)

// TestRetryOnlyClearsFailedTasks pins the rule that keeps a retry from handing
// the same targets out twice: a failed or missing task goes back in the queue,
// a running one does not, and the page says how many rows went back.
func TestRetryOnlyClearsFailedTasks(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) {
		d.ScanURL = "http://scanner.example.com"
		d.ScanKey = config.Secret("ssk_test")
	})
	ctx := t.Context()
	cands := func(target, platform string) []store.ScanCandidate {
		return []store.ScanCandidate{{Target: target, Platform: platform, Handle: "opensea"}}
	}
	if err := e.store.MarkHandedOff(ctx, cands("a.example.com", "bugcrowd"), "task-failed"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkHandedOff(ctx, cands("b.example.com", "bugcrowd"), "task-running"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetHandoffState(ctx, "task-failed", "failed", 4, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetHandoffState(ctx, "task-running", "running", 1, time.Now()); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"task": {"task-failed", "task-running"}}
	res, _ := e.do(e.client(), "POST", "/scan/retry", form, nil)
	if res.StatusCode != 303 {
		t.Fatalf("retry = %d, want a redirect back to the page", res.StatusCode)
	}
	loc := res.Header.Get("Location")
	res, body := e.do(e.client(), "GET", loc, nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("page after retry = %d (%s)", res.StatusCode, loc)
	}

	n, _, err := e.store.ScanHandoffCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("handoffs left = %d, want only the running one", n)
	}
	if !strings.Contains(body, "1 target(s) are back in the queue.") {
		t.Error("the page does not say what the retry did")
	}
}
