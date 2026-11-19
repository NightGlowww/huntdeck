package store

import "context"

// IgnoredCount is how many programs the user ignores, whatever the list's
// filters: an empty list says so, and links to them.
func (s *Store) IgnoredCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM programs WHERE ignored = 1").Scan(&n)
	return n, err
}
