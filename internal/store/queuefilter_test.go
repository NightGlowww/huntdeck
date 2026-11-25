package store

import (
	"testing"
	"time"
)

// TestQueueScannedFilterSplitsTheTwoSides pins the queue's scanned filter: with
// hundreds of targets handed over, "which of these has the scanner taken" has to
// be answerable - and the two sides must partition the queue, not overlap or drop
// a row.
func TestQueueScannedFilterSplitsTheTwoSides(t *testing.T) {
	q := newQueueTest(t)
	ctx := q.ctx
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	q.seed(t, queueSeedRows(time.Now()))
	if err := q.s.RefreshHuntQueue(ctx, now); err != nil {
		t.Fatal(err)
	}

	all, _, err := q.s.HuntQueueTop(ctx, QueueFilter{}, 2000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 2 {
		t.Fatalf("the fixture has %d rows", len(all))
	}

	// Hand one over.
	taken := all[0]
	if err := q.s.MarkHandedOff(ctx, []ScanCandidate{{Target: taken.Value, Platform: taken.Platform, Handle: taken.Handle}}, "task-1"); err != nil {
		t.Fatal(err)
	}

	yes, _, err := q.s.HuntQueueTop(ctx, QueueFilter{Scanned: "yes"}, 2000, 0)
	if err != nil {
		t.Fatal(err)
	}
	no, _, err := q.s.HuntQueueTop(ctx, QueueFilter{Scanned: "no"}, 2000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(yes) != 1 || yes[0].Value != taken.Value {
		t.Fatalf("scanned=yes returned %d rows, want just the one handed over", len(yes))
	}
	if len(yes)+len(no) != len(all) {
		t.Fatalf("%d + %d rows, want the %d the unfiltered queue has", len(yes), len(no), len(all))
	}
	for _, r := range no {
		if r.Value == taken.Value {
			t.Error("the handed-over target appears on both sides")
		}
	}
}
