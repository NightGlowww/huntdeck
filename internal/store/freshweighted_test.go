package store

import (
	"testing"
	"time"
)

// Quality takes the fresh part's contribution off the total, not the raw part.
// The weight is stored with the row so SQL does not need the preset (and so the
// store does not have to import huntscore, which would be a cycle).
func TestFreshWeightedIsStoredWithTheScore(t *testing.T) {
	s := newTestStore(t, nil)
	ctx := t.Context()
	if _, err := s.db.W.ExecContext(ctx, `INSERT INTO programs (platform, handle, name, url, state, offers_bounty, private, first_seen_at, last_seen_at)
		VALUES ('fake','acme','Acme','https://example.com','open',1,0,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := s.db.R.QueryRowContext(ctx, `SELECT id FROM programs WHERE handle = 'acme'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	sc := HuntScore{ProgramID: id, Detail: HuntDetail{
		Score: 51.1,
		At:    time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC),
		Parts: []HuntPart{
			{Key: "fresh", Score: 10.5, Weight: 0.35, Known: true},
			{Key: "competition", Score: 36.0, Weight: 0.30, Known: true},
		},
	}}
	if err := s.SaveHuntScores(ctx, []HuntScore{sc}, nil); err != nil {
		t.Fatal(err)
	}
	var got float64
	if err := s.db.R.QueryRowContext(ctx, `SELECT fresh_weighted FROM hunt_scores WHERE program_id = ?`, id).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if want := 10.5 * 0.35; got < want-0.001 || got > want+0.001 {
		t.Errorf("fresh_weighted = %v, want %v", got, want)
	}
}
