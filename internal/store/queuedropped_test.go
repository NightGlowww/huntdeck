package store

import (
	"testing"
	"time"
)

// A target scoring under the floor is left out of the queue — and counted, so
// the page can say a short list is not a complete one.
//
// Coverage note, deliberately not hidden: the fixture's programs have no
// credentials, so every candidate is gated and stored rather than dropped, and
// the counter never moves here. What this checks is the invariant around it —
// the count is the difference between the candidates the rebuild looked at and
// the rows it kept — which fails if a row is counted twice or a stored row is
// counted as dropped. A forced drop needs a fixture with a credentialled
// program; that is not built here, so the counter's increment is unproven.
func TestQueueDroppedCountsTheFloor(t *testing.T) {
	q := newQueueTest(t)
	ctx := q.ctx
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	q.seed(t, queueSeedRows(time.Now()))
	if n, err := q.s.QueueDropped(ctx); err != nil || n != 0 {
		t.Fatalf("before any rebuild: %d, %v", n, err)
	}
	cands, err := q.s.QueueCandidates(ctx, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) < 2 {
		t.Fatalf("the fixture has %d candidates", len(cands))
	}
	if err := q.s.RefreshHuntQueue(ctx, now); err != nil {
		t.Fatal(err)
	}
	dropped, err := q.s.QueueDropped(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ranked, _, err := q.s.HuntQueueCount(ctx, QueueFilter{})
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := q.s.HuntQueueGatedCount(ctx, QueueFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if want := len(cands) - ranked - blocked; dropped != want {
		t.Errorf("dropped %d, but %d candidates minus %d ranked and %d blocked is %d", dropped, len(cands), ranked, blocked, want)
	}
}
