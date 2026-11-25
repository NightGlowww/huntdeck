package store

import (
	"context"
	"testing"
	"time"
)

// TestHandoffTasksPrefersNeverRead pins the order that was still wrong after the
// first fix: "not finished" included tasks read a minute ago as queued, so every
// pass re-read the same eight and the 301 never-read rows were never reached.
// Never-read comes first now, whatever its age.
func TestHandoffTasksPrefersNeverRead(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	clock := start
	s := newTestStore(t, func() time.Time { return clock })

	// Finished and read, newest.
	clock = start
	if err := s.MarkHandedOff(ctx, []ScanCandidate{{Target: "done.example.com", Platform: "bugcrowd", Handle: "opensea"}}, "task-done"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetHandoffState(ctx, "task-done", "queued", 0, clock); err != nil {
		t.Fatal(err)
	}
	// Never read, older.
	clock = start.Add(-6 * time.Hour)
	if err := s.MarkHandedOff(ctx, []ScanCandidate{{Target: "unread.example.com", Platform: "bugcrowd", Handle: "opensea"}}, "task-unread"); err != nil {
		t.Fatal(err)
	}

	tasks, err := s.HandoffTasks(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].TaskID != "task-unread" {
		t.Fatalf("first task = %+v, want the never-read one", tasks)
	}
}
