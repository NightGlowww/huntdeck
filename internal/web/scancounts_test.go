package web

import (
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/store"
)

// TestScanPageCountsTheStates pins the histogram on the page: how many targets
// sit in each state, and how fresh that column is. It lived in a CLI subcommand
// for nine rounds, where nobody but the operator could see it.
func TestScanPageCountsTheStates(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) {
		d.ScanURL = "http://scanner.example.com"
		d.ScanKey = config.Secret("ssk_test")
	})
	ctx := t.Context()
	seed := func(target, task string) {
		t.Helper()
		if err := e.store.MarkHandedOff(ctx, []store.ScanCandidate{{Target: target, Platform: "bugcrowd", Handle: "opensea"}}, task); err != nil {
			t.Fatal(err)
		}
	}
	seed("done.example.com", "task-done")
	seed("run.example.com", "task-run")
	seed("unread.example.com", "task-unread")
	if err := e.store.SetHandoffState(ctx, "task-done", "done", 3, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetHandoffState(ctx, "task-run", "running", 1, time.Now()); err != nil {
		t.Fatal(err)
	}

	res, body := e.do(e.client(), "GET", "/scan", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("scan page = %d", res.StatusCode)
	}
	for _, want := range []string{"finished", "running", "not read yet", "last read"} {
		if !strings.Contains(body, want) {
			t.Errorf("the head does not mention %q", want)
		}
	}
}
