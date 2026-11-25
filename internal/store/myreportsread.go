package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// MyReport is one of the user's reports as stored, with its payments.
type MyReport struct {
	ID                int64
	Platform          string
	Key               string
	ProgramID         *int64
	ProgramHandle     string // the stored program's when it's known, else the platform's
	ProgramName       string
	ProgramPrivate    bool
	Title             string
	URL               string
	State             string
	RawState          string
	Severity          string
	SeverityLabel     string
	SubmittedSeverity string
	SeverityChanged   bool
	CVSS              *float64
	SubmittedAt       time.Time
	FirstResponseAt   *time.Time
	TriagedAt         *time.Time
	FirstBountyAt     *time.Time
	ClosedAt          *time.Time
	LastActivityAt    *time.Time
	LastProgramAt     *time.Time
	LastHunterAt      *time.Time
	WaitingOn         string
	Comments          int
	Unread            bool
	DuplicateOf       string
	Source            string
	GoneAt            *time.Time
	SnoozedUntil      *time.Time
	Muted             bool
	Edits             map[string]bool
	FirstSeenAt       time.Time
	UpdatedAt         time.Time
	Payments          []MyPayment
}

// MyPayment is money a report earned.
type MyPayment struct {
	ID        int64
	Ref       string
	Kind      string
	Amount    float64
	Currency  string
	AwardedAt time.Time
	PaidAt    *time.Time
	Status    string
}

// MyReportEvent is one step of a report's timeline.
type MyReportEvent struct {
	At   time.Time
	Kind string
	From string
	To   string
}

// ReportFilter narrows MyReports. Zero values match everything.
type ReportFilter struct {
	Platform  string
	ProgramID int64
	IDs       []int64
}

const myReportCols = `r.id, r.platform, r.report_key, r.program_id, COALESCE(p.handle, r.program_handle),
	CASE WHEN json_extract(r.edits, '$.program_name') OR p.name IS NULL THEN r.program_name ELSE p.name END,
	COALESCE(p.private, 0), r.title, r.url, r.state, r.raw_state, r.severity, r.severity_label, r.submitted_severity,
	r.severity_changed, r.cvss_score, r.submitted_at, r.first_response_at, r.triaged_at, r.first_bounty_at, r.closed_at,
	r.last_activity_at, r.last_program_activity_at, r.last_hunter_activity_at, r.waiting_on, r.comments, r.unread,
	r.duplicate_of, r.source, r.gone_at, r.snoozed_until, r.muted, r.edits, r.first_seen_at, r.updated_at`

func scanMyReport(sc interface{ Scan(...any) error }) (MyReport, error) {
	var r MyReport
	var programID sql.NullInt64
	var cvss sql.NullFloat64
	var submitted, seen, updated, edits string
	var firstResp, triaged, firstBounty, closed, lastAct, lastProg, lastHunter, gone, snoozed sql.NullString
	err := sc.Scan(&r.ID, &r.Platform, &r.Key, &programID, &r.ProgramHandle, &r.ProgramName, &r.ProgramPrivate, &r.Title, &r.URL,
		&r.State, &r.RawState, &r.Severity, &r.SeverityLabel, &r.SubmittedSeverity, &r.SeverityChanged, &cvss, &submitted,
		&firstResp, &triaged, &firstBounty, &closed, &lastAct, &lastProg, &lastHunter, &r.WaitingOn, &r.Comments, &r.Unread,
		&r.DuplicateOf, &r.Source, &gone, &snoozed, &r.Muted, &edits, &seen, &updated)
	if err != nil {
		return r, err
	}
	if programID.Valid {
		r.ProgramID = &programID.Int64
	}
	if cvss.Valid {
		r.CVSS = &cvss.Float64
	}
	r.SubmittedAt, r.FirstSeenAt, r.UpdatedAt = parseTS(submitted), parseTS(seen), parseTS(updated)
	r.FirstResponseAt, r.TriagedAt, r.FirstBountyAt = parseTSPtr(firstResp), parseTSPtr(triaged), parseTSPtr(firstBounty)
	r.ClosedAt, r.LastActivityAt, r.LastProgramAt = parseTSPtr(closed), parseTSPtr(lastAct), parseTSPtr(lastProg)
	r.LastHunterAt, r.GoneAt, r.SnoozedUntil = parseTSPtr(lastHunter), parseTSPtr(gone), parseTSPtr(snoozed)
	r.Edits = parseEdits(edits)
	return r, nil
}

// MyReports returns the user's reports matching f, newest submitted
// first, with their payments.
func (s *Store) MyReports(ctx context.Context, f ReportFilter) ([]MyReport, error) {
	var where []string
	var args []any
	if f.Platform != "" {
		where, args = append(where, "r.platform = ?"), append(args, f.Platform)
	}
	if f.ProgramID != 0 {
		where, args = append(where, "r.program_id = ?"), append(args, f.ProgramID)
	}
	if len(f.IDs) > 0 {
		b, _ := json.Marshal(f.IDs)
		where, args = append(where, "r.id IN (SELECT value FROM json_each(?))"), append(args, string(b))
	}
	q := `SELECT ` + myReportCols + ` FROM my_reports r LEFT JOIN programs p ON p.id = r.program_id`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	rows, err := s.db.R.QueryContext(ctx, q+" ORDER BY r.submitted_at DESC, r.id DESC", args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []MyReport
	idx := map[int64]int{}
	for rows.Next() {
		r, err := scanMyReport(rows)
		if err != nil {
			return nil, err
		}
		idx[r.ID] = len(out)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	pq := `SELECT y.id, y.report_id, y.ref, y.kind, y.amount, y.currency, y.awarded_at, y.paid_at, y.status
		FROM my_report_payments y JOIN my_reports r ON r.id = y.report_id`
	if len(where) > 0 {
		pq += " WHERE " + strings.Join(where, " AND ")
	}
	prows, err := s.db.R.QueryContext(ctx, pq+" ORDER BY y.awarded_at, y.id", args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = prows.Close() }()
	for prows.Next() {
		var p MyPayment
		var reportID int64
		var awarded string
		var paid sql.NullString
		if err := prows.Scan(&p.ID, &reportID, &p.Ref, &p.Kind, &p.Amount, &p.Currency, &awarded, &paid, &p.Status); err != nil {
			return nil, err
		}
		p.AwardedAt, p.PaidAt = parseTS(awarded), parseTSPtr(paid)
		if i, ok := idx[reportID]; ok {
			out[i].Payments = append(out[i].Payments, p)
			// Where the platform gives no first bounty (a report added
			// by hand), it's the first bounty paid: payments come oldest
			// first.
			if out[i].FirstBountyAt == nil && p.Kind == "bounty" {
				at := p.AwardedAt
				out[i].FirstBountyAt = &at
			}
		}
	}
	return out, prows.Err()
}

// MyReportByID returns one report with its payments.
func (s *Store) MyReportByID(ctx context.Context, id int64) (MyReport, error) {
	rs, err := s.MyReports(ctx, ReportFilter{IDs: []int64{id}})
	if err != nil {
		return MyReport{}, err
	}
	if len(rs) == 0 {
		return MyReport{}, ErrNotFound
	}
	return rs[0], nil
}

// MyReportEvents returns a report's timeline, oldest first.
func (s *Store) MyReportEvents(ctx context.Context, id int64) ([]MyReportEvent, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT at, kind, from_value, to_value FROM my_report_events
		WHERE report_id = ? ORDER BY at, rowid`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []MyReportEvent
	for rows.Next() {
		var e MyReportEvent
		var at string
		if err := rows.Scan(&at, &e.Kind, &e.From, &e.To); err != nil {
			return nil, err
		}
		e.At = parseTS(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountMyReports is how many reports are stored (any source).
func (s *Store) CountMyReports(ctx context.Context) (int, error) {
	var n int
	err := s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM my_reports`).Scan(&n)
	return n, err
}

// ReportInput is a report entered or edited by hand. On an edit of a
// synced report, only the fields in Set are changed, and kept from then on.
type ReportInput struct {
	Platform          string
	ProgramID         *int64
	ProgramName       string
	Title             string
	URL               string
	State             string
	Severity          string
	SubmittedSeverity string
	SubmittedAt       time.Time
	ClosedAt          *time.Time
	Payments          []MyPayment
	// Set names the fields given, for an edit (EditableReportFields, plus
	// "payments" for a manual report).
	Set []string
}

// AddManualReport stores a report entered by hand and returns its ID.
func (s *Store) AddManualReport(ctx context.Context, in ReportInput) (int64, error) {
	if in.Title == "" || in.SubmittedAt.IsZero() || in.State == "" {
		return 0, errors.New("a report needs a title, a submission date and a state")
	}
	var id int64
	err := s.Write(ctx, func(tx *Tx) error {
		var handle, name string
		if in.ProgramID == nil && in.ProgramName != "" {
			// A program HuntDeck has, by its name on that platform.
			id, err := tx.reportProgram(ctx, in.Platform, "", in.ProgramName)
			if err != nil {
				return err
			}
			in.ProgramID = id
		}
		if in.ProgramID != nil {
			if err := tx.tx.QueryRowContext(ctx, `SELECT handle, name FROM programs WHERE id = ?`, *in.ProgramID).Scan(&handle, &name); err != nil {
				return fmt.Errorf("program: %w", err)
			}
		}
		if in.ProgramName != "" {
			name = in.ProgramName
		}
		err := tx.tx.QueryRowContext(ctx, `INSERT INTO my_reports (platform, report_key, program_id, program_handle, program_name, title,
			url, state, severity, submitted_severity, submitted_at, closed_at, source, first_seen_at, updated_at)
			VALUES (?1, 'manual-' || (SELECT COALESCE(MAX(id), 0) + 1 FROM my_reports), ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11,
			'manual', ?12, ?12) RETURNING id`,
			in.Platform, in.ProgramID, handle, name, in.Title, in.URL, in.State, in.Severity, in.SubmittedSeverity,
			ts(in.SubmittedAt), tsPtr(in.ClosedAt), tx.now).Scan(&id)
		if err != nil {
			return err
		}
		return tx.setManualPayments(ctx, id, in.Payments)
	})
	return id, err
}

// UpdateReport changes the fields in.Set names. On a synced report they're
// recorded as set by hand, so syncs keep them; payments can be changed
// only on a report added by hand.
func (s *Store) UpdateReport(ctx context.Context, id int64, in ReportInput) error {
	return s.Write(ctx, func(tx *Tx) error {
		var source, edits string
		if err := tx.tx.QueryRowContext(ctx, `SELECT source, edits FROM my_reports WHERE id = ?`, id).Scan(&source, &edits); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		set := parseEdits(edits)
		vals := map[string]any{"title": in.Title, "program_name": in.ProgramName, "url": in.URL, "state": in.State,
			"severity": in.Severity, "submitted_severity": in.SubmittedSeverity}
		if !in.SubmittedAt.IsZero() {
			vals["submitted_at"] = ts(in.SubmittedAt)
		}
		for _, f := range in.Set {
			v, ok := vals[f]
			q, editable := reportFieldUpdates[f]
			if !ok || !editable {
				continue
			}
			if _, err := tx.tx.ExecContext(ctx, q, v, tx.now, id); err != nil {
				return err
			}
			if source == ReportFromPlatform {
				set[f] = true
			}
		}
		if source == ReportByHand {
			if in.ProgramID == nil && in.ProgramName != "" {
				var platformName string
				if err := tx.tx.QueryRowContext(ctx, `SELECT platform FROM my_reports WHERE id = ?`, id).Scan(&platformName); err != nil {
					return err
				}
				pid, err := tx.reportProgram(ctx, platformName, "", in.ProgramName)
				if err != nil {
					return err
				}
				in.ProgramID = pid
			}
			if in.ProgramID != nil {
				if _, err := tx.tx.ExecContext(ctx, `UPDATE my_reports SET program_id = ?, program_handle = COALESCE((SELECT handle FROM programs WHERE id = ?), '')
					WHERE id = ?`, *in.ProgramID, *in.ProgramID, id); err != nil {
					return err
				}
			}
			if _, err := tx.tx.ExecContext(ctx, `UPDATE my_reports SET closed_at = ? WHERE id = ?`, tsPtr(in.ClosedAt), id); err != nil {
				return err
			}
			if slices.Contains(in.Set, "payments") {
				if _, err := tx.tx.ExecContext(ctx, `DELETE FROM my_report_payments WHERE report_id = ?`, id); err != nil {
					return err
				}
				if err := tx.setManualPayments(ctx, id, in.Payments); err != nil {
					return err
				}
			}
		}
		b, _ := json.Marshal(set)
		_, err := tx.tx.ExecContext(ctx, `UPDATE my_reports SET edits = ? WHERE id = ?`, string(b), id)
		return err
	})
}

// reportFieldUpdates sets each of EditableReportFields.
var reportFieldUpdates = map[string]string{
	"title":              `UPDATE my_reports SET title = ?, updated_at = ? WHERE id = ?`,
	"program_name":       `UPDATE my_reports SET program_name = ?, updated_at = ? WHERE id = ?`,
	"url":                `UPDATE my_reports SET url = ?, updated_at = ? WHERE id = ?`,
	"state":              `UPDATE my_reports SET state = ?, updated_at = ? WHERE id = ?`,
	"severity":           `UPDATE my_reports SET severity = ?, updated_at = ? WHERE id = ?`,
	"submitted_severity": `UPDATE my_reports SET submitted_severity = ?, updated_at = ? WHERE id = ?`,
	"submitted_at":       `UPDATE my_reports SET submitted_at = ?, updated_at = ? WHERE id = ?`,
}

// ResetReportEdits forgets what the user set by hand on a synced report:
// the next sync brings the platform's values back.
func (s *Store) ResetReportEdits(ctx context.Context, id int64) error {
	return s.Write(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `UPDATE my_reports SET edits = '{}', marker = '' WHERE id = ? AND source = 'platform'`, id)
		return err
	})
}

func (t *Tx) setManualPayments(ctx context.Context, id int64, ps []MyPayment) error {
	for _, p := range ps {
		if p.Amount <= 0 || p.Currency == "" || p.AwardedAt.IsZero() {
			continue
		}
		kind := p.Kind
		if kind == "" {
			kind = "bounty"
		}
		if _, err := t.tx.ExecContext(ctx, `INSERT INTO my_report_payments (report_id, kind, amount, currency, awarded_at, paid_at, status)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, id, kind, p.Amount, strings.ToUpper(p.Currency), ts(p.AwardedAt), tsPtr(p.PaidAt), p.Status); err != nil {
			return err
		}
	}
	return nil
}

// DeleteManualReport removes a report added by hand. Synced reports can't
// be deleted (the next sync would bring them back); mute hides them.
func (s *Store) DeleteManualReport(ctx context.Context, id int64) error {
	return s.Write(ctx, func(tx *Tx) error {
		res, err := tx.tx.ExecContext(ctx, `DELETE FROM my_reports WHERE id = ? AND source = 'manual'`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// SnoozeReport hides a report from "needs attention" until until (nil
// wakes it).
func (s *Store) SnoozeReport(ctx context.Context, id int64, until *time.Time) error {
	return s.Write(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `UPDATE my_reports SET snoozed_until = ? WHERE id = ?`, tsPtr(until), id)
		return err
	})
}

// MuteReport turns a report's notifications and attention flags off or on.
func (s *Store) MuteReport(ctx context.Context, id int64, muted bool) error {
	return s.Write(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `UPDATE my_reports SET muted = ? WHERE id = ?`, muted, id)
		return err
	})
}

// PublishedTime are a program's published averages, in hours (nil where
// the platform doesn't publish one), from its latest stats.
type PublishedTime struct {
	FirstResponse, Triage, Bounty, Resolution, Validation *float64
}

// PublishedTimes returns the latest published response times of the
// programs given, by ID: for comparing the user's own times with them.
func (s *Store) PublishedTimes(ctx context.Context, ids []int64) (map[int64]PublishedTime, error) {
	out := map[int64]PublishedTime{}
	if len(ids) == 0 {
		return out, nil
	}
	b, _ := json.Marshal(ids)
	rows, err := s.db.R.QueryContext(ctx, `SELECT p.id, ps.first_response_hours, ps.triage_hours, ps.bounty_hours, ps.resolution_hours,
		ps.validation_hours FROM programs p JOIN program_public_stats ps ON ps.program_id = p.id AND ps.captured_at = p.stats_at
		WHERE p.id IN (SELECT value FROM json_each(?))`, string(b))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var fr, tr, bo, re, va sql.NullFloat64
		if err := rows.Scan(&id, &fr, &tr, &bo, &re, &va); err != nil {
			return nil, err
		}
		out[id] = PublishedTime{FirstResponse: nullF(fr), Triage: nullF(tr), Bounty: nullF(bo), Resolution: nullF(re), Validation: nullF(va)}
	}
	return out, rows.Err()
}

func nullF(v sql.NullFloat64) *float64 {
	if !v.Valid || v.Float64 <= 0 {
		return nil
	}
	return &v.Float64
}
