package web

import (
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/store"
)

// TestReadNowTooltipFollowsTheSetting pins the sentence to the configured batch:
// the button said "eight" while eight was a constant, and making the batch a
// setting would have left the tooltip lying about what a press does.
func TestReadNowTooltipFollowsTheSetting(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) {
		d.ScanURL = "http://scanner.example.com"
		d.ScanKey = config.Secret("ssk_test")
		d.ScanReadBatch = 20
	})
	// The button lives in the panel that lists recent handoffs, so one has to exist.
	if err := e.store.MarkHandedOff(t.Context(), []store.ScanCandidate{{Target: "a.example.com", Platform: "bugcrowd", Handle: "opensea"}}, "task-1"); err != nil {
		t.Fatal(err)
	}
	_, body := e.do(e.client(), "GET", "/scan", nil, nil)
	if !strings.Contains(body, "next 20 tasks") && !strings.Contains(body, "接下来 20 个任务") {
		t.Error("the tooltip does not name the configured batch")
	}
	if strings.Contains(body, "next eight tasks") || strings.Contains(body, "接下来八个任务") {
		t.Error("the tooltip still says eight")
	}
}
