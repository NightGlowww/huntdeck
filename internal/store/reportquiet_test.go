package store

import (
	"context"
	"testing"
	"time"
)

// TestQuietReportsAnnouncesOnce pins the gone-quiet rule: a report undecided
// for longer than the days says so once, not every tick, and a word from the
// program makes it eligible again the next time it goes quiet.
func TestQuietReportsAnnouncesOnce(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })

	add := func(key, state string, activity time.Time) int64 {
		t.Helper()
		var id int64
		if err := s.Write(ctx, func(tx *Tx) error {
			return tx.tx.QueryRowContext(ctx, `INSERT INTO my_reports
				(platform, report_key, title, state, submitted_at, last_activity_at, source, first_seen_at, updated_at)
				VALUES ('hackerone', ?, ?, ?, ?, ?, 'platform', ?, ?) RETURNING id`,
				key, "Silent "+key, state, ts(now.Add(-40*24*time.Hour)), ts(activity), tx.now, tx.now).Scan(&id)
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}

	id := add("q-1", "new", now.Add(-20*24*time.Hour))

	got, err := s.QuietReports(ctx, 14, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("quiet reports = %+v, want the one silent report", got)
	}

	// Stamped: the same silence is not announced twice. The stamp is when the
	// notice went out - after that activity, so nothing is due again.
	if err := s.MarkQuietNotified(ctx, []int64{id}, now.Add(-18*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if again, err := s.QuietReports(ctx, 14, now); err != nil || len(again) != 0 {
		t.Fatalf("second read = %+v, %v; want nothing", again, err)
	}

	// A word from the program makes it eligible again, and it goes quiet again
	// once that word is older than the days too.
	if err := s.Write(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `UPDATE my_reports SET last_activity_at = ? WHERE id = ?`,
			ts(now.Add(-15*24*time.Hour)), id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if after, err := s.QuietReports(ctx, 14, now); err != nil || len(after) != 1 {
		t.Fatalf("after new activity = %+v, %v; want it quiet again", after, err)
	}

	// A decided report is never quiet: the program answered.
	if err := s.Write(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `UPDATE my_reports SET state = 'accepted' WHERE id = ?`, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if done, err := s.QuietReports(ctx, 14, now); err != nil || len(done) != 0 {
		t.Fatalf("decided report = %+v, %v; want nothing", done, err)
	}
}
