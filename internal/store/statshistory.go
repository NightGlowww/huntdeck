package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// DayValue is one day's value of a stats number.
type DayValue struct {
	At    time.Time
	Value float64
}

// ReportsReceivedHistory returns each program's daily "reports received in
// the last 90 days" since since, oldest first. Programs without the number
// get no entry.
func (s *Store) ReportsReceivedHistory(ctx context.Context, programIDs []int64, since time.Time) (map[int64][]DayValue, error) {
	return s.dailyStat(ctx, "reports_90d", programIDs, since)
}

// ReportsMonthHistory returns each program's daily "reports received this
// calendar month so far" since since, oldest first. Programs without the
// number get no entry.
func (s *Store) ReportsMonthHistory(ctx context.Context, programIDs []int64, since time.Time) (map[int64][]DayValue, error) {
	return s.dailyStat(ctx, "reports_month", programIDs, since)
}

// PaidHistory returns each program's daily "bounties paid in the last 90
// days" since since, oldest first, in each snapshot's currency. Programs
// without the number get no entry.
func (s *Store) PaidHistory(ctx context.Context, programIDs []int64, since time.Time) (map[int64][]DayValue, error) {
	return s.dailyStat(ctx, "bounties_paid_90d", programIDs, since)
}

// TotalsHistory is programs' saved all-time totals, oldest first, each
// where the platform publishes it.
type TotalsHistory struct {
	Reports, Paid, Rewarded, Accepted map[int64][]DayValue
}

// TotalsHistory returns each program's saved all-time totals since since:
// reports received, bounties paid, rewarded reports and accepted reports.
func (s *Store) TotalsHistory(ctx context.Context, programIDs []int64, since time.Time) (TotalsHistory, error) {
	var h TotalsHistory
	for _, c := range []struct {
		column string
		into   *map[int64][]DayValue
	}{{"total_reports", &h.Reports}, {"bounties_paid", &h.Paid}, {"rewarded_reports", &h.Rewarded}, {"accepted_reports", &h.Accepted}} {
		var err error
		if *c.into, err = s.dailyStat(ctx, c.column, programIDs, since); err != nil {
			return h, err
		}
	}
	return h, nil
}

// AvgPayoutHistory returns each program's daily average payout (Bugcrowd's
// over the last three months; the high end of HackerOne's average bounty)
// since since, oldest first.
func (s *Store) AvgPayoutHistory(ctx context.Context, programIDs []int64, since time.Time) (map[int64][]DayValue, error) {
	return s.dailyStat(ctx, "avg_bounty_high", programIDs, since)
}

// dailyStat returns one column of each program's snapshots since since,
// oldest first. column is a constant from this package, never user input.
func (s *Store) dailyStat(ctx context.Context, column string, programIDs []int64, since time.Time) (map[int64][]DayValue, error) {
	out := map[int64][]DayValue{}
	if len(programIDs) == 0 {
		return out, nil
	}
	args := []any{ts(since)}
	for _, id := range programIDs {
		args = append(args, id)
	}
	//nolint:gosec // only placeholders are concatenated; values are bound
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT program_id, captured_at, `+column+` FROM program_public_stats
		WHERE captured_at >= ? AND `+column+` IS NOT NULL AND program_id IN (`+placeholders(len(programIDs))+`)
		ORDER BY program_id, captured_at`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var at string
		var v float64
		if err := rows.Scan(&id, &at, &v); err != nil {
			return nil, err
		}
		out[id] = append(out[id], DayValue{parseTS(at), v})
	}
	return out, rows.Err()
}

// ProgramSnapshots are the daily numbers a programs list shows, read in one
// pass: the 90-day figures, the average payout and the all-time totals.
// Reading them column by column meant one scan of program_public_stats for
// each: seven for a page of thirty programs.
type ProgramSnapshots struct {
	Reports90d, Paid90d, AvgPayout    map[int64][]DayValue
	Reports, Paid, Rewarded, Accepted map[int64][]DayValue
}

// SnapshotsFor reads every daily column of programIDs since since, oldest
// first, in one query.
func (s *Store) SnapshotsFor(ctx context.Context, programIDs []int64, since time.Time) (ProgramSnapshots, error) {
	out := ProgramSnapshots{
		Reports90d: map[int64][]DayValue{}, Paid90d: map[int64][]DayValue{}, AvgPayout: map[int64][]DayValue{},
		Reports: map[int64][]DayValue{}, Paid: map[int64][]DayValue{}, Rewarded: map[int64][]DayValue{}, Accepted: map[int64][]DayValue{},
	}
	if len(programIDs) == 0 {
		return out, nil
	}
	args := []any{ts(since)}
	for _, id := range programIDs {
		args = append(args, id)
	}
	const columns = `program_id, captured_at, reports_90d, bounties_paid_90d, avg_bounty_high,
		total_reports, bounties_paid, rewarded_reports, accepted_reports`
	//nolint:gosec // only placeholders are concatenated; values are bound
	q := `SELECT ` + columns + ` FROM program_public_stats WHERE captured_at >= ? AND program_id IN (` + placeholders(len(programIDs)) + `)
		ORDER BY program_id, captured_at`
	rows, err := s.db.R.QueryContext(ctx, q, args...)
	if err != nil {
		return out, fmt.Errorf("read program snapshots: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var at string
		var r90, p90, avg, total, paid, rewarded, accepted sql.NullFloat64
		if err := rows.Scan(&id, &at, &r90, &p90, &avg, &total, &paid, &rewarded, &accepted); err != nil {
			return out, err
		}
		t := parseTS(at)
		add := func(m map[int64][]DayValue, v sql.NullFloat64) {
			if v.Valid {
				m[id] = append(m[id], DayValue{t, v.Float64})
			}
		}
		add(out.Reports90d, r90)
		add(out.Paid90d, p90)
		add(out.AvgPayout, avg)
		add(out.Reports, total)
		add(out.Paid, paid)
		add(out.Rewarded, rewarded)
		add(out.Accepted, accepted)
	}
	return out, rows.Err()
}
