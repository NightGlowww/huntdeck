package store

import (
	"context"
	"fmt"
	"time"
)

// QuietReport is a report that has gone quiet: undecided, and no word from
// the program for longer than the days Settings › Reports sets.
type QuietReport struct {
	ID             int64
	Title          string
	URL            string
	State          string
	Severity       string
	Platform       string
	ProgramName    string
	ProgramHandle  string
	ProgramPrivate bool
	LastActivity   time.Time
	QuietDays      int
}

// QuietReports finds the reports that have just gone quiet: undecided, no
// activity for `days`, and no notice sent since that activity. A report the
// user muted, snoozed, added by hand and closed, or one the platform no longer
// lists, is left out: it is not a program being silent, it is a decision.
func (s *Store) QuietReports(ctx context.Context, days int, now time.Time) ([]QuietReport, error) {
	if days < 1 {
		return nil, nil
	}
	cutoff := now.UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT id, title, url, state, severity, platform, program_name, program_handle,
		       0, COALESCE(last_activity_at, submitted_at), quiet_notified_at
		  FROM my_reports r
		 WHERE r.state IN ('new', 'triaged')
		   AND r.gone_at IS NULL
		   AND COALESCE(r.muted, 0) = 0
		   AND (r.snoozed_until IS NULL OR r.snoozed_until < ?)
		   AND COALESCE(r.last_activity_at, r.submitted_at) < ?
		   AND (r.quiet_notified_at IS NULL OR r.quiet_notified_at < COALESCE(r.last_activity_at, r.submitted_at))
		 ORDER BY COALESCE(r.last_activity_at, r.submitted_at) ASC
		 LIMIT 200`, now.UTC().Format(time.RFC3339), cutoff)
	if err != nil {
		return nil, fmt.Errorf("quiet reports: %w", err)
	}
	defer rows.Close()
	var out []QuietReport
	for rows.Next() {
		var q QuietReport
		var activity string
		var notified *string
		if err := rows.Scan(&q.ID, &q.Title, &q.URL, &q.State, &q.Severity, &q.Platform,
			&q.ProgramName, &q.ProgramHandle, &q.ProgramPrivate, &activity, &notified); err != nil {
			return nil, fmt.Errorf("quiet reports: %w", err)
		}
		if t, err := time.Parse(time.RFC3339, activity); err == nil {
			q.LastActivity = t
		}
		q.QuietDays = days
		out = append(out, q)
	}
	return out, rows.Err()
}

// MarkQuietNotified records that the gone-quiet notice went out, so the same
// silence is not announced twice.
func (s *Store) MarkQuietNotified(ctx context.Context, ids []int64, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	return s.Write(ctx, func(tx *Tx) error {
		stamp := at.UTC().Format(time.RFC3339)
		for _, id := range ids {
			if _, err := tx.tx.ExecContext(ctx, `UPDATE my_reports SET quiet_notified_at = ? WHERE id = ?`, stamp, id); err != nil {
				return fmt.Errorf("mark quiet: %w", err)
			}
		}
		return nil
	})
}
