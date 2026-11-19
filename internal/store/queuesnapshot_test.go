package store

import (
	"testing"
	"time"
)

// The queue table is replaced on every rebuild, so without a snapshot there is
// nothing to compare a past day against. It has to survive a second rebuild on
// the same day without duplicating rows.
func TestQueueSnapshotKeepsTheHead(t *testing.T) {
	q := newQueueTest(t)
	s, ctx := q.s, q.ctx
	id := q.ids["acme"]
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	rows := []HuntQueueRow{
		{ProgramID: id, Kind: "domain", Value: "a.example.com", Score: 70},
		{ProgramID: id, Kind: "domain", Value: "b.example.com", Score: 90},
		{ProgramID: id, Kind: "domain", Value: "c.example.com", Score: 50},
	}
	if err := s.Write(ctx, func(tx *Tx) error { return snapshotQueueHead(ctx, tx, rows, now) }); err != nil {
		t.Fatal(err)
	}
	// A later rebuild the same day, with a changed score, upserts.
	rows[0].Score = 95
	if err := s.Write(ctx, func(tx *Tx) error { return snapshotQueueHead(ctx, tx, rows, now.Add(30*time.Minute)) }); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM queue_scores").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("rows = %d, want 3 (a same-day rebuild upserts)", n)
	}
	var best float64
	if err := s.db.R.QueryRowContext(ctx, "SELECT score FROM queue_scores WHERE value = 'a.example.com'").Scan(&best); err != nil {
		t.Fatal(err)
	}
	if best != 95 {
		t.Fatalf("score = %v, want the later rebuild's 95", best)
	}
	days, err := s.QueueDays(ctx)
	if err != nil || len(days) != 1 {
		t.Fatalf("days = %v, %v", days, err)
	}
}
