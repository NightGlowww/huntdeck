package store

import (
	"context"
	"testing"
	"time"
)

// TestHandoffTasksReadsTheBacklogFirst pins the order the reconciler walks: a
// finished task is not worth asking about again, and a limit over a newest-first
// list kept the pass on the same already-read tasks forever - 301 rows stayed
// unread while every pass reported success.
func TestHandoffTasksReadsTheBacklogFirst(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	// A clock we move, so the two handoffs get different created_at values: with
	// one instant for both, newest-first and backlog-first are the same query.
	clock := start
	s := newTestStore(t, func() time.Time { return clock })

	clock = start.Add(-48 * time.Hour)
	if err := s.MarkHandedOff(ctx, []ScanCandidate{{Target: "old.example.com", Platform: "bugcrowd", Handle: "opensea"}}, "task-old"); err != nil {
		t.Fatal(err)
	}
	clock = start.Add(-1 * time.Hour)
	if err := s.MarkHandedOff(ctx, []ScanCandidate{{Target: "new.example.com", Platform: "bugcrowd", Handle: "opensea"}}, "task-new"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetHandoffState(ctx, "task-new", "done", 3, clock); err != nil {
		t.Fatal(err)
	}

	tasks, err := s.HandoffTasks(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("%d tasks, want 1", len(tasks))
	}
	if tasks[0].TaskID != "task-old" {
		t.Errorf("first task = %s, want the unread one (task-old)", tasks[0].TaskID)
	}
}
