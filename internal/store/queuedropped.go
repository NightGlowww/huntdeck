package store

import (
	"context"
)

// QueueDropped is how many targets the last rebuild left out for being below
// the score floor, and when that rebuild ran. Zero is a real answer: it means
// the list the page shows is everything that was considered.
func (s *Store) QueueDropped(ctx context.Context) (int, error) {
	var n int
	err := s.db.R.QueryRowContext(ctx,
		`SELECT below_floor FROM queue_dropped ORDER BY day DESC LIMIT 1`).Scan(&n)
	if err != nil {
		return 0, nil // nothing rebuilt yet
	}
	return n, nil
}
