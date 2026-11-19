package store

import (
	"testing"
)

// The added window reads target_scores, so the dedupe path must join it too:
// without the join the query fails at runtime (it did, as a 500).
func TestOnePerProgramWithTheAddedWindow(t *testing.T) {
	q := newQueueTest(t)
	f := QueueFilter{OnePerProgram: true, Added: "7d"}
	if _, _, err := q.s.HuntQueueTop(q.ctx, f, 10, 0); err != nil {
		t.Fatalf("one-per-program with an added window: %v", err)
	}
	if _, _, err := q.s.HuntQueueCount(q.ctx, f); err != nil {
		t.Fatalf("count with an added window: %v", err)
	}
	f.Kind = "url"
	if _, _, err := q.s.HuntQueueTop(q.ctx, f, 10, 0); err != nil {
		t.Fatalf("added + kind + one-per-program: %v", err)
	}
}
