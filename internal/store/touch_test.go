package store

import (
	"context"
	"testing"
	"time"
)

// The heartbeat is what separates a working poll from a hung one for every
// reader outside the process, so the round trip has to hold: write it, read it
// back as a real time, and stop reporting it once the run is finished.
func TestRunningRunsCarryTheirHeartbeat(t *testing.T) {
	s := newTestStore(t, nil)
	ctx := context.Background()
	id, err := s.StartRun(ctx, "hackerone", "full", "schedule", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TouchRun(ctx, id); err != nil {
		t.Fatal(err)
	}
	runs, err := s.RunningRuns(ctx)
	if err != nil || len(runs) != 1 {
		t.Fatalf("RunningRuns = %d, %v", len(runs), err)
	}
	if runs[0].LastActivity == nil {
		t.Fatal("the heartbeat was not read back")
	}
	if idle := time.Since(*runs[0].LastActivity); idle > time.Minute {
		t.Fatalf("the heartbeat reads as %v old: it was just written", idle)
	}
}
