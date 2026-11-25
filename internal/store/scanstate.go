package store

import (
	"context"
	"fmt"
	"time"
)

// HandoffTask is one scanner task HuntDeck created, as the reconciler needs it:
// which task, and what was last read about it.
type HandoffTask struct {
	TaskID    string
	Targets   int
	State     string // queued, running, done, failed, unknown; '' when never read
	Status    int    // the scanner's own number, -1 when never read
	CheckedAt *time.Time
}

// HandoffTasks lists the tasks HuntDeck has created, newest first, with how
// many targets each holds and what the scanner last said about it.
func (s *Store) HandoffTasks(ctx context.Context, limit int) ([]HandoffTask, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT task_id, COUNT(*), MIN(state), MIN(status), MIN(checked_at)
		  FROM scan_handoffs
		 WHERE task_id <> ''
		 GROUP BY task_id
		 ORDER BY MAX(created_at) DESC
		 LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("handoff tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []HandoffTask
	for rows.Next() {
		var t HandoffTask
		var checked *string
		if err := rows.Scan(&t.TaskID, &t.Targets, &t.State, &t.Status, &checked); err != nil {
			return nil, fmt.Errorf("handoff tasks: %w", err)
		}
		if checked != nil {
			if at, err := time.Parse(time.RFC3339, *checked); err == nil {
				t.CheckedAt = &at
			}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetHandoffState records what the scanner said about one task, for every
// target it carried.
func (s *Store) SetHandoffState(ctx context.Context, taskID, state string, status int, at time.Time) error {
	return s.Write(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `UPDATE scan_handoffs SET state = ?, status = ?, checked_at = ? WHERE task_id = ?`,
			state, status, at.UTC().Format(time.RFC3339), taskID)
		if err != nil {
			return fmt.Errorf("set handoff state: %w", err)
		}
		return nil
	})
}

// HandoffStates counts the targets by state, for the scan page and doctor.
func (s *Store) HandoffStates(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT state, COUNT(*) FROM scan_handoffs GROUP BY state`)
	if err != nil {
		return nil, fmt.Errorf("handoff states: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, fmt.Errorf("handoff states: %w", err)
		}
		out[st] = n
	}
	return out, rows.Err()
}
