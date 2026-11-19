package store

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// Memo keeps the results of reads too slow to repeat on every page view
// (the Stats pages' month and day series, a list's facet counts) until
// anything at all is committed to the database, by this process or
// another, or MemoTTL passes. SQLite's data_version, read on a connection
// of its own, says when something was: unlike count_version (cachedCount)
// it needs no triggers kept in step with what's read, at the price of
// starting again after every poll.
type memoCache struct {
	mu       sync.Mutex
	conn     *sql.Conn // held: data_version only compares on one connection
	gen      int64     // data_version the entries were read at; -1 for none
	m        map[string]memoEntry
	inflight map[string]*memoCall
}

type memoEntry struct {
	v  any
	at time.Time
}

// memoCall is a result being worked out; others wanting it wait for it.
type memoCall struct {
	done chan struct{}
	v    any
	err  error
}

const (
	// MemoTTL bounds how old a memoised result gets while nothing
	// changes: results that depend on the time (this month so far, the
	// last 90 days) are at most this far behind it.
	MemoTTL = 10 * time.Minute
	// maxMemos bounds the cache; it is cleared when full.
	maxMemos = 64
)

// dataVersion reads the database's data version; c.mu is held. A new
// connection's numbers don't compare with the old one's, so getting one
// clears the cache.
func (s *Store) dataVersion(ctx context.Context) (int64, error) {
	c := &s.memo
	if c.conn == nil {
		conn, err := s.db.R.Conn(ctx)
		if err != nil {
			return 0, err
		}
		c.conn, c.gen, c.m = conn, -1, nil
	}
	var v int64
	if err := c.conn.QueryRowContext(ctx, `PRAGMA data_version`).Scan(&v); err != nil {
		_ = c.conn.Close()
		c.conn, c.gen, c.m = nil, -1, nil
		return 0, err
	}
	return v, nil
}

// DataVersion is a number that changes whenever anything is committed to
// the database.
func (s *Store) DataVersion(ctx context.Context) (int64, error) {
	s.memo.mu.Lock()
	defer s.memo.mu.Unlock()
	return s.dataVersion(ctx)
}

// Memo returns fn's result for key: the one worked out last, unless the
// database has changed since or it's older than MemoTTL. The key must
// name everything the result depends on except the current time, which
// MemoTTL covers. Results are shared between callers, which mustn't
// modify them. Callers wanting a result being worked out wait for it, so
// fn's context isn't cancelled with its caller's.
func Memo[T any](ctx context.Context, s *Store, key string, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	c := &s.memo
	c.mu.Lock()
	gen, err := s.dataVersion(ctx)
	if err != nil {
		c.mu.Unlock()
		return zero, err
	}
	now := s.now()
	if c.m == nil || c.gen != gen || len(c.m) >= maxMemos {
		c.gen, c.m = gen, map[string]memoEntry{}
	}
	if e, ok := c.m[key]; ok && now.Sub(e.at) < MemoTTL {
		// A key belongs to one type. If two callers disagree, drop the entry
		// and work it out again rather than panicking on a bad cast: a
		// mismatch is a bug to fix, not an outage to serve.
		if v, ok := e.v.(T); ok {
			c.mu.Unlock()
			return v, nil
		}
		delete(c.m, key)
	}
	if call, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-call.done:
		case <-ctx.Done():
			return zero, ctx.Err()
		}
		if call.err != nil {
			return zero, call.err
		}
		return call.v.(T), nil
	}
	call := &memoCall{done: make(chan struct{})}
	if c.inflight == nil {
		c.inflight = map[string]*memoCall{}
	}
	c.inflight[key] = call
	c.mu.Unlock()

	call.err = errMemoPanic // until fn returns
	defer func() {
		c.mu.Lock()
		delete(c.inflight, key)
		if call.err == nil && c.gen == gen && c.m != nil {
			c.m[key] = memoEntry{v: call.v, at: now}
		}
		c.mu.Unlock()
		close(call.done)
	}()
	// Shared by every caller waiting on it, so one leaving doesn't stop it;
	// but nothing runs on unbounded.
	mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), memoTimeout)
	defer cancel()
	v, err := fn(mctx)
	call.v, call.err = v, err
	return v, err
}

// memoTimeout is the longest a memoised result is worked out for.
const memoTimeout = 30 * time.Second

// errMemoPanic is what callers waiting on a result get if working it out
// panicked.
var errMemoPanic = errors.New("memo: working out the result failed")
