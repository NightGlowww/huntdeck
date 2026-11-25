package store

import (
	"testing"
	"time"
)

// TestQueueCandidateCarriesTheScanState pins the join between the handoff table
// and the queue: a target that was handed over carries what the scanner said, so
// the queue's first reason can say 已扫完 rather than a generic 已派发.
func TestQueueCandidateCarriesTheScanState(t *testing.T) {
	q := newQueueTest(t)
	ctx := q.ctx
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	q.seed(t, queueSeedRows(time.Now()))

	before, err := q.s.QueueCandidates(ctx, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) == 0 {
		t.Fatal("the fixture has no candidates")
	}
	target := before[0]

	// Hand it over and read back what the scanner said.
	if err := q.s.MarkHandedOff(ctx, []ScanCandidate{{Target: target.Value, Platform: target.Platform, Handle: target.Handle}}, "task-1"); err != nil {
		t.Fatal(err)
	}
	if err := q.s.SetHandoffState(ctx, "task-1", "done", 3, now); err != nil {
		t.Fatal(err)
	}

	after, err := q.s.QueueCandidates(ctx, 2000)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range after {
		if c.Value != target.Value || c.Kind != target.Kind {
			continue
		}
		if !c.HandedOff {
			t.Error("the target was handed over but the queue does not say so")
		}
		if c.ScanState != "done" {
			t.Errorf("scan state = %q, want done", c.ScanState)
		}
		return
	}
	t.Fatalf("%s is not among the candidates after the handoff", target.Value)
}
