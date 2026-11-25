package web

import (
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/store"
)

// TestScanPageShowsWhatTheScannerSaid pins the last link where a reader can see
// it: the handoff table carries a state column, filled from the scanner's
// answer, and says so plainly when a task has not been read yet.
func TestScanPageShowsWhatTheScannerSaid(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) {
		d.ScanURL = "http://scanner.example.com"
		d.ScanKey = config.Secret("ssk_test")
	})
	ctx := t.Context()
	seeded := []store.ScanCandidate{{Target: "a.example.com", Platform: "bugcrowd", Handle: "opensea"}}
	if err := e.store.MarkHandedOff(ctx, seeded, "task-done"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkHandedOff(ctx, []store.ScanCandidate{{Target: "b.example.com", Platform: "bugcrowd", Handle: "opensea"}}, "task-new"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetHandoffState(ctx, "task-done", "done", 3, time.Now()); err != nil {
		t.Fatal(err)
	}

	res, body := e.do(e.client(), "GET", "/scan", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("scan page = %d", res.StatusCode)
	}
	for _, want := range []string{"task-done", "done", "task-new", "not read yet"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not show %q", want)
		}
	}
}
