// Package store holds all of HuntDeck's SQL. Other packages call typed
// methods; none of them write SQL themselves.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/db"
)

// ErrNotFound is returned when a lookup matches nothing.
var ErrNotFound = errors.New("not found")

// TimeFormat is how timestamps are stored: fixed width, UTC, so they sort
// correctly as text.
const TimeFormat = "2006-01-02T15:04:05.000Z"

// Store wraps the database.
type Store struct {
	db     *db.DB
	now    func() time.Time
	counts countCache
	memo   memoCache
}

// countCache keeps expensive list totals until stored data changes.
type countCache struct {
	mu  sync.Mutex
	gen int64
	m   map[string]any
}

// maxCachedCounts bounds the cache; it is cleared when full.
const maxCachedCounts = 256

// cachedCount returns the count for key, computing it with fn unless it was
// computed since the data last changed. Triggers bump count_version
// whenever a row or column the counts read changes (migration 0017), so
// polls that change nothing keep the cache.
func (s *Store) cachedCount(ctx context.Context, key string, fn func() (int, error)) (int, error) {
	return cached(ctx, s, key, fn)
}

// A cached count is only as fresh as count_version, which triggers bump on
// programs, scope_entries and scope_targets (see 0017_count_version.sql).
// Nothing bumps it for program_public_stats, so a count that depends on a
// stats column (top bounty, reports received, resolutions) must not go through
// here: it would keep the number from before the stats were fetched. Filters
// over programs' own columns (state, private, bounty flag, removed) and over
// targets are safe.

// cached is cachedCount for any value: facet counts as well as totals.
func cached[T any](ctx context.Context, s *Store, key string, fn func() (T, error)) (T, error) {
	var zero T
	var gen int64
	err := s.db.R.QueryRowContext(ctx, `SELECT n FROM count_version`).Scan(&gen)
	if err != nil {
		return zero, err
	}
	c := &s.counts
	c.mu.Lock()
	if c.m == nil || c.gen != gen || len(c.m) >= maxCachedCounts {
		c.gen, c.m = gen, map[string]any{}
	}
	v, ok := c.m[key].(T)
	c.mu.Unlock()
	if ok {
		return v, nil
	}
	if v, err = fn(); err != nil {
		return zero, err
	}
	c.mu.Lock()
	if c.gen == gen {
		c.m[key] = v
	}
	c.mu.Unlock()
	return v, nil
}

// New returns a Store. now may be nil (defaults to time.Now).
func New(d *db.DB, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{db: d, now: now}
}

// Now returns the store's current time in UTC.
func (s *Store) Now() time.Time { return s.now().UTC() }

// Tx is a write transaction. Its methods all use the same timestamp, so
// every row written in one transaction agrees on "now".
type Tx struct {
	tx  *sql.Tx
	now string
}

// Write runs fn in a write transaction, committing if fn returns nil.
func (s *Store) Write(ctx context.Context, fn func(*Tx) error) (err error) {
	sqlTx, err := s.db.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if err != nil {
			if rbErr := sqlTx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				err = errors.Join(err, rbErr)
			}
		}
	}()
	if err = fn(&Tx{tx: sqlTx, now: ts(s.Now())}); err != nil {
		return err
	}
	if err = sqlTx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func ts(t time.Time) string { return t.UTC().Format(TimeFormat) }

func tsPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}

func parseTS(s string) time.Time {
	t, _ := time.Parse(TimeFormat, s)
	return t
}

func parseTSPtr(s sql.NullString) *time.Time {
	if !s.Valid {
		return nil
	}
	t := parseTS(s.String)
	return &t
}

// boolPtr stores an optional flag: NULL, 0 or 1.
func boolPtr(b *bool) *int {
	if b == nil {
		return nil
	}
	v := b2i(*b)
	return &v
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// placeholders returns "?, ?, ?" for n parameters.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// Now is the transaction's time, as it stamps what it writes.
func (t *Tx) Now() time.Time { return parseTS(t.now) }
