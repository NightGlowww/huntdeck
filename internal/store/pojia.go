package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PojiaProfile is one program's cached disclosed-report history from POJIA.
type PojiaProfile struct {
	ProgramID int64
	Handle    string
	Profile   string
	Found     bool
	FetchedAt time.Time
}

var ErrPojiaUnknown = errors.New("no cached profile")

// PojiaProfileFor reads the cached answer for one program.
func (s *Store) PojiaProfileFor(ctx context.Context, programID int64) (*PojiaProfile, error) {
	var p PojiaProfile
	var found int
	var at string
	err := s.db.R.QueryRowContext(ctx,
		`SELECT program_id, handle, profile, found, fetched_at FROM pojia_profiles WHERE program_id = ?`,
		programID).Scan(&p.ProgramID, &p.Handle, &p.Profile, &found, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPojiaUnknown
	}
	if err != nil {
		return nil, err
	}
	p.Found = found == 1
	p.FetchedAt, _ = time.Parse(time.RFC3339, at)
	return &p, nil
}

// SavePojiaProfile caches one answer. A profile older than a week is fetched
// again on the next visit; POJIA's history changes slowly.
func (s *Store) SavePojiaProfile(ctx context.Context, p PojiaProfile) error {
	return s.Write(ctx, func(tx *Tx) error {
		found := 0
		if p.Found {
			found = 1
		}
		_, err := tx.tx.ExecContext(ctx, `INSERT INTO pojia_profiles
			(program_id, handle, profile, found, fetched_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (program_id) DO UPDATE SET handle = excluded.handle,
			 profile = excluded.profile, found = excluded.found, fetched_at = excluded.fetched_at`,
			p.ProgramID, p.Handle, p.Profile, found, s.Now().UTC().Format(time.RFC3339))
		return err
	})
}
