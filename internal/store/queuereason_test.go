package store

import (
	"testing"
	"time"
)

// TestMaterialisedQueueKeepsTheScanState pins the bridge the queue page actually
// reads: RefreshHuntQueue builds a huntqueue.Inputs per candidate by hand, and
// a field left out of that literal is lost silently - the page showed
// "handed over, unread" for every row because ScanState never got there, while
// the query and the reasons both handled it.
func TestMaterialisedQueueKeepsTheScanState(t *testing.T) {
	q := newQueueTest(t)
	ctx := q.ctx
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	q.seed(t, queueSeedRows(time.Now()))

	_, _, err := q.s.HuntQueueTop(ctx, QueueFilter{}, 2000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.s.RefreshHuntQueue(ctx, now); err != nil {
		t.Fatal(err)
	}
	before, _, err := q.s.HuntQueueTop(ctx, QueueFilter{}, 2000, 0)
	if err != nil || len(before) == 0 {
		t.Fatalf("no rows: %v", err)
	}
	target := before[0]

	if err := q.s.MarkHandedOff(ctx, []ScanCandidate{{Target: target.Value, Platform: target.Platform, Handle: target.Handle}}, "task-1"); err != nil {
		t.Fatal(err)
	}
	if err := q.s.SetHandoffState(ctx, "task-1", "done", 3, now); err != nil {
		t.Fatal(err)
	}
	if err := q.s.RefreshHuntQueue(ctx, now); err != nil {
		t.Fatal(err)
	}

	rows, _, err := q.s.HuntQueueTop(ctx, QueueFilter{}, 2000, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Value != target.Value {
			continue
		}
		if len(r.Reasons) == 0 {
			t.Fatal("the row has no reasons")
		}
		want := "already scanned: the scanner finished it"
		if r.Reasons[0].Key != want {
			t.Errorf("first reason = %q, want %q (the scan state did not reach the queue)", r.Reasons[0].Key, want)
		}
		return
	}
	t.Fatalf("%s is not in the materialised queue", target.Value)
}
