package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// ResearcherStats holds the user's stored researcher standing for a platform.
type ResearcherStats struct {
	Platform   string
	CapturedAt time.Time
	platform.ResearcherStats
}

// SaveResearcherStats persists the user's own researcher stats for platformName.
func (s *Store) SaveResearcherStats(ctx context.Context, platformName string, rs platform.ResearcherStats) error {
	raw, err := json.Marshal(rs)
	if err != nil {
		return fmt.Errorf("save researcher stats %s: %w", platformName, err)
	}
	now := s.Now().UTC().Format(time.RFC3339)
	_, err = s.db.W.ExecContext(ctx, `
		INSERT INTO researcher_stats (platform, username, captured_at, data)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(platform) DO UPDATE SET
			username    = excluded.username,
			captured_at = excluded.captured_at,
			data        = excluded.data
	`, platformName, rs.Username, now, string(raw))
	if err != nil {
		return fmt.Errorf("save researcher stats %s: %w", platformName, err)
	}
	return nil
}

// ResearcherStats returns the stored researcher stats for platformName, or
// (nil, nil) if none have been saved yet.
func (s *Store) ResearcherStats(ctx context.Context, platformName string) (*ResearcherStats, error) {
	var username, capturedAt, raw string
	err := s.db.R.QueryRowContext(ctx, `
		SELECT username, captured_at, data
		FROM researcher_stats
		WHERE platform = ?
	`, platformName).Scan(&username, &capturedAt, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("researcher stats %s: %w", platformName, err)
	}
	out := &ResearcherStats{Platform: platformName}
	if err := json.Unmarshal([]byte(raw), &out.ResearcherStats); err != nil {
		return nil, fmt.Errorf("researcher stats %s: decode: %w", platformName, err)
	}
	if out.Username == "" {
		out.Username = username
	}
	if t, err := time.Parse(time.RFC3339, capturedAt); err == nil {
		out.CapturedAt = t.UTC()
	}
	return out, nil
}
