package store

import (
	"context"
	"database/sql"
	"time"
)

// ReportNotice is news on one of the user's reports, waiting for the
// notifier (my_report_changes), with the report as it is now.
type ReportNotice struct {
	ID         int64
	Kind       string // ReportChange kinds
	From, To   string
	Amount     *float64 // a bounty's
	Currency   string
	OccurredAt time.Time
	Report     MyReport
}

// reportNoticesAtOnce caps the notices read at a time.
const reportNoticesAtOnce = 500

// ReportNotices returns the news on the user's reports not yet queued,
// oldest first.
func (s *Store) ReportNotices(ctx context.Context) ([]ReportNotice, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, report_id, kind, from_value, to_value, amount, currency, occurred_at
		FROM my_report_changes ORDER BY id LIMIT ?`, reportNoticesAtOnce)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ReportNotice
	var ids []int64
	for rows.Next() {
		var n ReportNotice
		var amount sql.NullFloat64
		var at string
		if err := rows.Scan(&n.ID, &n.Report.ID, &n.Kind, &n.From, &n.To, &amount, &n.Currency, &at); err != nil {
			return nil, err
		}
		if amount.Valid {
			n.Amount = &amount.Float64
		}
		n.OccurredAt = parseTS(at)
		out = append(out, n)
		ids = append(ids, n.Report.ID)
	}
	if err := rows.Err(); err != nil || len(out) == 0 {
		return nil, err
	}
	reports, err := s.MyReports(ctx, ReportFilter{IDs: ids})
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]MyReport, len(reports))
	for _, r := range reports {
		byID[r.ID] = r
	}
	for i := range out {
		out[i].Report = byID[out[i].Report.ID]
	}
	return out, nil
}

// QueueReportNotices queues the notifications made from the notices ids
// and lets go of the notices, in one transaction.
func (s *Store) QueueReportNotices(ctx context.Context, ids []int64, outbox []OutboxMessage, pending []PendingEvent) error {
	return s.Write(ctx, func(tx *Tx) error {
		if err := tx.queue(ctx, outbox, pending); err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := tx.tx.ExecContext(ctx, `DELETE FROM my_report_changes WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	})
}
