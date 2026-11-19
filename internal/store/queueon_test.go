package store

import (
	"testing"
)

// A day's queue head comes back best first, and a day with no snapshot is
// empty rather than an error: the backtest asks for every score day, including
// the ones before the queue started keeping history.
func TestQueueOn(t *testing.T) {
	q := newQueueTest(t)
	ctx := q.ctx
	id := q.ids["acme"]
	for _, r := range []struct {
		value string
		score float64
	}{{"a.example.com", 40}, {"b.example.com", 90}, {"c.example.com", 60}} {
		if _, err := q.s.db.W.ExecContext(ctx,
			`INSERT INTO queue_scores (day, program_id, kind, value, score) VALUES ('2026-11-19', ?, 'domain', ?, ?)`,
			id, r.value, r.score); err != nil {
			t.Fatal(err)
		}
	}
	got, err := q.s.QueueOn(ctx, "2026-11-19")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Value != "b.example.com" || got[2].Value != "a.example.com" {
		t.Fatalf("order = %+v, want best first", got)
	}
	if empty, err := q.s.QueueOn(ctx, "2026-01-01"); err != nil || len(empty) != 0 {
		t.Fatalf("a day with no snapshot = %v, %v", empty, err)
	}
}
