package store

import (
	"context"
	"testing"
	"time"
)

// A run killed with its process keeps status=running and holds its platform: the
// doctor showed an Intigriti quick poll "running 1h" with no activity for 56m.
func TestOrphanedRunsAreClosedAtStartup(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	id, err := s.StartRun(ctx, "intigriti", "quick", "schedule", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if running, err := s.RunningRuns(ctx); err != nil || len(running) == 0 {
		t.Fatalf("the run is not running before the sweep: %v %v", running, err)
	}

	n, err := s.FailOrphanedRuns(ctx, s.Now())
	if err != nil || n != 1 {
		t.Fatalf("swept %d, %v", n, err)
	}
	if running, _ := s.RunningRuns(ctx); len(running) != 0 {
		t.Fatalf("a run is still running: %v", running)
	}
	runs, err := s.RecentRuns(ctx, 5)
	if err != nil || len(runs) == 0 {
		t.Fatalf("runs: %v %v", runs, err)
	}
	if runs[0].ID != id || runs[0].Status != RunFailed || runs[0].Error != RunInterrupted {
		t.Fatalf("the run was not closed as interrupted: %+v", runs[0])
	}
	if runs[0].FinishedAt == nil {
		t.Fatal("no finish time")
	}

	// Twice is harmless: there is nothing left to close.
	if n, err := s.FailOrphanedRuns(ctx, s.Now()); err != nil || n != 0 {
		t.Fatalf("second sweep touched %d rows, %v", n, err)
	}
}
