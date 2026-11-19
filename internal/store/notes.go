package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// NoteMax is the longest note HuntDeck keeps: generous, but bounded, so a
// paste accident cannot fill the database.
const NoteMax = 100_000

// ProgramNote returns a program's note ("" when it has none) and when it was
// last saved.
func (s *Store) ProgramNote(ctx context.Context, programID int64) (string, time.Time, error) {
	var note, at string
	err := s.db.R.QueryRowContext(ctx, "SELECT note, updated_at FROM program_notes WHERE program_id = ?", programID).Scan(&note, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read program note: %w", err)
	}
	t := parseTS(at)
	return note, t, nil
}

// SetProgramNote writes a program's note; an empty one removes the row, so
// "has a note" stays a cheap join.
func (s *Store) SetProgramNote(ctx context.Context, programID int64, note string) error {
	note = strings.TrimSpace(note)
	if len(note) > NoteMax {
		note = note[:NoteMax]
	}
	now := ts(s.now())
	return s.Write(ctx, func(tx *Tx) error {
		if note == "" {
			_, err := tx.tx.ExecContext(ctx, "DELETE FROM program_notes WHERE program_id = ?", programID)
			return err
		}
		_, err := tx.tx.ExecContext(ctx,
			"INSERT INTO program_notes (program_id, note, updated_at) VALUES (?, ?, ?) ON CONFLICT (program_id) DO UPDATE SET note = excluded.note, updated_at = excluded.updated_at",
			programID, note, now)
		return err
	})
}

// NotedPrograms are the programs with a note, for the marker beside their
// names and the rail's "Has notes" filter.
func (s *Store) NotedPrograms(ctx context.Context) (map[int64]bool, error) {
	out := map[int64]bool{}
	rows, err := s.db.R.QueryContext(ctx, "SELECT program_id FROM program_notes")
	if err != nil {
		return nil, fmt.Errorf("list noted programs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
