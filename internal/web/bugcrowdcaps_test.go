package web

import (
	"context"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform/bugcrowd"
)

// The page must not wait on bugcrowd.com: one render starts the fetch and
// shows nothing, the next shows the answer, and a stale entry refreshes once
// rather than once per render.
func TestFillBugcrowdCapsNeverBlocksThePage(t *testing.T) {
	e := newTestEnv(t)
	calls := make(chan struct{}, 8)
	srv := &server{Deps: Deps{Store: e.store, BugcrowdCaps: func(context.Context) (bugcrowd.SubmissionCaps, bool) {
		calls <- struct{}{}
		return bugcrowd.SubmissionCaps{WeeklyRemaining: 4, WeeklyCap: 6}, true
	}}}

	rows := []platformRow{{Name: "bugcrowd"}}
	srv.fillBugcrowdCaps(context.Background(), rows)
	if rows[0].CapsCap != 0 {
		t.Fatalf("the first render waited for the answer: %+v", rows[0])
	}

	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("no refresh was started")
	}

	// The background fetch has stored by now.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		srv.fillBugcrowdCaps(context.Background(), rows)
		if rows[0].CapsCap == 6 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rows[0].CapsRemaining != 4 || rows[0].CapsCap != 6 {
		t.Fatalf("the answer never showed: %+v", rows[0])
	}

	// Cached: no further calls while it is fresh.
	before := len(calls)
	srv.fillBugcrowdCaps(context.Background(), rows)
	if len(calls) != before {
		t.Fatal("a fresh cache still fetched")
	}
}
