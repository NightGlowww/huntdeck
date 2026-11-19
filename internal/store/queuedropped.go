package store

import (
	"context"
	"database/sql"
	"errors"
)

// QueueDropped is how many targets the last rebuild left out for scoring under
// the floor. Zero is a real answer only once a rebuild has run: with none yet it
// returns 0 and a nil error, and a read failure returns the error — so a caller
// can tell "nothing was dropped" from "the count is unavailable", which is the
// whole reason the row is written.
func (s *Store) QueueDropped(ctx context.Context) (int, error) {
	var n int
	err := s.db.R.QueryRowContext(ctx,
		`SELECT below_floor FROM queue_dropped ORDER BY day DESC LIMIT 1`).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return n, nil
}
