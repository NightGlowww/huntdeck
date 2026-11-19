package store

import (
	"testing"
	"time"
)

// A target scoring under the floor is left out of the queue — and counted, so
// the page can say a short list is not a complete one.
func TestQueueDroppedCountsTheFloor(t *testing.T) {
	q := newQueueTest(t)
	ctx := q.ctx
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	if n, err := q.s.QueueDropped(ctx); err != nil || n != 0 {
		t.Fatalf("before any rebuild: %d, %v", n, err)
	}
	if err := q.s.RefreshHuntQueue(ctx, now); err != nil {
		t.Fatal(err)
	}
	n, err := q.s.QueueDropped(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n < 0 {
		t.Fatalf("below floor = %d", n)
	}
	var stored int
	if err := q.s.db.R.QueryRowContext(ctx, `SELECT below_floor FROM queue_dropped`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != n {
		t.Fatalf("stored %d, read back %d", stored, n)
	}
}
