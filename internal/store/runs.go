package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrRunInProgress means another poll of the same platform holds the lease.
var ErrRunInProgress = errors.New("a poll of this platform is already running")

// Run statuses.
const (
	RunRunning = "running"
	RunOK      = "ok"
	RunPartial = "partial"
	RunFailed  = "failed"
)

// RunInterrupted is the error recorded on a run stopped by shutdown or
// cancellation; it is a failed run, but not a platform problem.
const RunInterrupted = "interrupted"

// DisplayStatus is the status to show people: "interrupted" for runs stopped
// by a shutdown, otherwise Status.
func (r Run) DisplayStatus() string {
	if r.Status == RunFailed && r.Error == RunInterrupted {
		return RunInterrupted
	}
	return r.Status
}

// Run is one row of poll_runs.
type Run struct {
	ID              int64
	Platform        string
	Mode            string
	Trigger         string
	Status          string
	ListingComplete bool
	ListingRecent   bool // only the newest programs were listed (see platform.RecentLister)
	StartedAt       time.Time
	FinishedAt      *time.Time
	// LastActivity is the last heartbeat a running poll left (see TouchRun).
	LastActivity    *time.Time
	ProgramsSeen    int
	ScopesFetched   int
	ScopesFailed    int
	ScopesUnchanged int // not fetched: the platform says they haven't changed
	StatsFetched    int // programs whose public stats were refreshed
	StatsFailed     int
	DisclosuresNew  int // disclosed reports first seen
	ResolutionsNew  int // resolved (undisclosed) reports first seen
	AcceptedNew     int // accepted (undisclosed) reports first seen
	Changes         int
	Requests        int64
	RateLimited     int64
	Error           string
}

// StartRun takes the platform's lease and records a running poll. A
// platform has two leases: one for its polls, and one for refreshes of
// single programs (TriggerProgram), which run beside a poll rather than
// wait for it. A running row of the same lease older than staleAfter is
// assumed abandoned (the process died) and is marked failed first.
func (s *Store) StartRun(ctx context.Context, platform, mode, trigger string, staleAfter time.Duration) (int64, error) {
	var id int64
	err := s.Write(ctx, func(tx *Tx) error {
		cutoff := ts(s.Now().Add(-staleAfter))
		if _, err := tx.tx.ExecContext(ctx, `
			UPDATE poll_runs SET status = 'failed', finished_at = ?, error = 'abandoned: process stopped before finishing'
			WHERE platform = ? AND status = 'running' AND started_at < ? AND (trigger = ?) = (? = ?)`,
			tx.now, platform, cutoff, TriggerProgram, trigger, TriggerProgram); err != nil {
			return err
		}
		res, err := tx.tx.ExecContext(ctx, `
			INSERT INTO poll_runs (platform, mode, trigger, status, started_at) VALUES (?, ?, ?, 'running', ?)`,
			platform, mode, trigger, tx.now)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return ErrRunInProgress
			}
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

// FinishRun records the outcome of a run and releases the lease.
func (s *Store) FinishRun(ctx context.Context, r Run) error {
	if r.Status == RunRunning || r.Status == "" {
		return fmt.Errorf("finish run %d: invalid status %q", r.ID, r.Status)
	}
	return s.Write(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `
			UPDATE poll_runs SET status = ?, listing_complete = ?, listing_recent = ?, finished_at = ?, programs_seen = ?,
				scopes_fetched = ?, scopes_failed = ?, scopes_unchanged = ?, stats_fetched = ?, stats_failed = ?, disclosures_new = ?, resolutions_new = ?, accepted_new = ?, changes = ?,
				requests = ?, rate_limited = ?, error = ?
			WHERE id = ?`,
			r.Status, b2i(r.ListingComplete), b2i(r.ListingRecent), tx.now, r.ProgramsSeen, r.ScopesFetched, r.ScopesFailed, r.ScopesUnchanged,
			r.StatsFetched, r.StatsFailed, r.DisclosuresNew, r.ResolutionsNew, r.AcceptedNew, r.Changes, r.Requests, r.RateLimited, r.Error, r.ID)
		return err
	})
}

// LastCompleteListing returns when the platform's latest poll with a
// complete listing started, or nil if none had one.
func (s *Store) LastCompleteListing(ctx context.Context, platform string) (*time.Time, error) {
	var at sql.NullString
	err := s.db.R.QueryRowContext(ctx, `SELECT MAX(started_at) FROM poll_runs
		WHERE platform = ? AND listing_complete = 1 AND status IN ('ok', 'partial')`, platform).Scan(&at)
	return parseTSPtr(at), err
}

// LastUpdate returns when the latest successful poll of any platform
// finished; where none has (a demo, or an install loaded from a seed),
// when a platform last listed any program; nil if neither.
func (s *Store) LastUpdate(ctx context.Context) (*time.Time, error) {
	var at sql.NullString
	err := s.db.R.QueryRowContext(ctx, `SELECT COALESCE((SELECT MAX(finished_at) FROM poll_runs WHERE status IN ('ok', 'partial') AND platform <> ?),
		(SELECT MAX(last_seen_at) FROM programs WHERE platform <> ?))`, Independent, Independent).Scan(&at)
	return parseTSPtr(at), err
}

// PlatformChecked is when HuntDeck last read each platform's listing: its
// latest poll to finish (ok or partial), else, on a copy loaded from a seed
// (which keeps no polls), when its programs were last seen listed. Programs
// added by hand aren't a platform to check.
type PlatformChecked struct {
	Platform string
	At       time.Time
}

// PlatformsChecked lists PlatformChecked for every platform with
// programs, by name.
func (s *Store) PlatformsChecked(ctx context.Context) ([]PlatformChecked, error) {
	var out []PlatformChecked
	err := s.each(ctx, `SELECT p.platform, COALESCE((SELECT MAX(r.finished_at) FROM poll_runs r
			WHERE r.platform = p.platform AND r.status IN ('ok', 'partial')), MAX(p.last_seen_at))
		FROM programs p WHERE p.platform <> ? GROUP BY p.platform ORDER BY p.platform`, []any{Independent}, func(rows *sql.Rows) error {
		var c PlatformChecked
		var at sql.NullString
		if err := rows.Scan(&c.Platform, &at); err != nil {
			return err
		}
		if t := parseTSPtr(at); t != nil {
			c.At = *t
			out = append(out, c)
		}
		return nil
	})
	return out, err
}

// TriggerProgram is the trigger of a poll refreshing only some programs
// (syncer.Options.Handles), such as one whose invitation was just accepted.
const TriggerProgram = "program"

// TriggerEdit is the trigger of a "poll" saving the programs added by hand
// (syncer.SaveOwn): the user's edit, not a platform's news.
const TriggerEdit = "edit"

// LastRun returns the most recent run of platform in mode, or nil.
// Refreshes of single programs (TriggerProgram) don't count: they don't
// stand in for the platform's scheduled polls.
func (s *Store) LastRun(ctx context.Context, platform, mode string) (*Run, error) {
	runs, err := s.queryRuns(ctx, `WHERE platform = ? AND mode = ? AND trigger <> ? ORDER BY started_at DESC, id DESC LIMIT 1`,
		platform, mode, TriggerProgram)
	if err != nil || len(runs) == 0 {
		return nil, err
	}
	return &runs[0], nil
}

// RecentRuns returns the latest runs across all platforms, newest first.
func (s *Store) RecentRuns(ctx context.Context, limit int) ([]Run, error) {
	return s.queryRuns(ctx, `ORDER BY started_at DESC, id DESC LIMIT ?`, limit)
}

const selectRuns = `
	SELECT id, platform, mode, trigger, status, listing_complete, listing_recent, started_at, finished_at, programs_seen,
		scopes_fetched, scopes_failed, scopes_unchanged, stats_fetched, stats_failed, disclosures_new, resolutions_new, accepted_new, changes, requests, rate_limited,
		error
	FROM poll_runs `

// queryRuns appends where, which is always a constant from this file; values
// go through args.
func (s *Store) queryRuns(ctx context.Context, where string, args ...any) ([]Run, error) {
	rows, err := s.db.R.QueryContext(ctx, selectRuns+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Run
	for rows.Next() {
		var r Run
		var started string
		var finished sql.NullString
		if err := rows.Scan(&r.ID, &r.Platform, &r.Mode, &r.Trigger, &r.Status, &r.ListingComplete, &r.ListingRecent, &started, &finished,
			&r.ProgramsSeen, &r.ScopesFetched, &r.ScopesFailed, &r.ScopesUnchanged, &r.StatsFetched, &r.StatsFailed, &r.DisclosuresNew, &r.ResolutionsNew, &r.AcceptedNew, &r.Changes, &r.Requests, &r.RateLimited, &r.Error); err != nil {
			return nil, err
		}
		r.StartedAt = parseTS(started)
		r.FinishedAt = parseTSPtr(finished)
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestRuns returns the most recent run for each platform and mode.
func (s *Store) LatestRuns(ctx context.Context) ([]Run, error) {
	return s.queryRuns(ctx, `WHERE id IN (SELECT MAX(id) FROM poll_runs GROUP BY platform, mode) ORDER BY platform, mode`)
}

// RunningSince returns the runs in progress that started at or after since
// (by this process: an older one is a crashed process's, left running).
func (s *Store) RunningSince(ctx context.Context, since time.Time) ([]Run, error) {
	return s.queryRuns(ctx, `WHERE status = 'running' AND started_at >= ? ORDER BY started_at`, ts(since))
}

// RunningRun returns the platform's in-progress poll, or nil. Refreshes
// of single programs (TriggerProgram) hold their own lease, so they don't
// count.
func (s *Store) RunningRun(ctx context.Context, platformName string) (*Run, error) {
	runs, err := s.queryRuns(ctx, `WHERE platform = ? AND status = 'running' AND trigger <> ? LIMIT 1`, platformName, TriggerProgram)
	if err != nil || len(runs) == 0 {
		return nil, err
	}
	return &runs[0], nil
}

// RecentDurations returns how long the platform's latest n finished runs
// of mode took (polls, or refreshes of single programs if program),
// newest first: what a running one is likely to take.
func (s *Store) RecentDurations(ctx context.Context, platform, mode string, program bool, n int) ([]time.Duration, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT started_at, finished_at FROM poll_runs
		WHERE platform = ? AND mode = ? AND (trigger = ?) = ? AND status IN ('ok', 'partial') AND finished_at IS NOT NULL
		ORDER BY id DESC LIMIT ?`, platform, mode, TriggerProgram, program, n)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []time.Duration
	for rows.Next() {
		var start, end string
		if err := rows.Scan(&start, &end); err != nil {
			return nil, err
		}
		if d := parseTS(end).Sub(parseTS(start)); d > 0 {
			out = append(out, d)
		}
	}
	return out, rows.Err()
}

// HasFinishedPoll reports whether any poll has finished (ok or partial): a
// copy loaded from a seed has none, and its "last checked" times are the
// seed's import, not a check.
func (s *Store) HasFinishedPoll(ctx context.Context) (bool, error) {
	var yes bool
	if err := s.db.R.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM poll_runs WHERE status IN ('ok', 'partial'))`).Scan(&yes); err != nil {
		return false, fmt.Errorf("check for finished polls: %w", err)
	}
	return yes, nil
}

// TouchRun records that a running poll just did something. It is the only
// liveness signal a database reader has while a poll runs: the counters are
// written when it ends.
func (s *Store) TouchRun(ctx context.Context, id int64) error {
	_, err := s.db.W.ExecContext(ctx,
		`UPDATE poll_runs SET last_activity = ? WHERE id = ? AND status = 'running'`,
		s.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("touch run: %w", err)
	}
	return nil
}

// RunningRuns are the polls in progress, with the last heartbeat each one left.
func (s *Store) RunningRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, platform, mode, started_at, last_activity
		FROM poll_runs WHERE status = 'running' ORDER BY started_at`)
	if err != nil {
		return nil, fmt.Errorf("running polls: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Run
	for rows.Next() {
		var r Run
		var started, activity sql.NullString
		if err := rows.Scan(&r.ID, &r.Platform, &r.Mode, &started, &activity); err != nil {
			return nil, fmt.Errorf("running polls: %w", err)
		}
		r.Status = RunRunning
		if at := parseTSPtr(started); at != nil {
			r.StartedAt = *at
		}
		r.LastActivity = parseTSPtr(activity)
		out = append(out, r)
	}
	return out, rows.Err()
}
