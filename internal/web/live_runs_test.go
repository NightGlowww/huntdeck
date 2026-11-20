package web

import (
	"context"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// A run left running by a process that has restarted has no in-memory
// progress, so the row's heartbeat is the only thing that says whether it
// is working. Without it the settings page shows a live poll with nothing
// to say, which is how a stuck one hides.
func TestLiveRunsFallsBackToTheStoredHeartbeat(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now()
	stale := now.Add(-10 * time.Minute)
	runs := []store.Run{{
		ID: 1, Platform: "hackerone", Mode: "full", Status: store.RunRunning,
		StartedAt: now.Add(-30 * time.Minute), LastActivity: &stale,
	}}
	srv := &server{Deps: Deps{Store: e.store}}
	live := srv.liveRuns(context.Background(), runs)
	l, ok := live[1]
	if !ok {
		t.Fatal("the running run is missing")
	}
	if !l.Stalled {
		t.Fatalf("a run with no heartbeat for ten minutes is not marked stalled: %+v", l)
	}
	if l.Idle == "" {
		t.Fatalf("idle time not shown: %+v", l)
	}
}
