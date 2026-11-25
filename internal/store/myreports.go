package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// The user's own reports (migration 0055): what syncs save. Reads and
// edits are in myreportsread.go.

// Report sources.
const (
	ReportFromPlatform = "platform"
	ReportByHand       = "manual"
)

// EditableReportFields are the fields the user can set by hand on a synced
// report; a sync never overwrites one they set (my_reports.edits).
var EditableReportFields = []string{"title", "program_name", "url", "state", "severity", "submitted_severity", "submitted_at"}

// ReportChange is something a sync found different on a report: a new
// report, a new state, a new severity, a bounty, or the program waiting on
// the user. Notifications are made from them.
type ReportChange struct {
	ReportID    int64
	Platform    string
	ProgramID   *int64
	ProgramName string
	Title       string
	URL         string
	Kind        string // new, state, severity, bounty, waiting
	From, To    string
	Amount      float64
	Currency    string
}

// Report change kinds.
const (
	ReportChangeNew      = "new"
	ReportChangeState    = "state"
	ReportChangeSeverity = "severity"
	ReportChangeBounty   = "bounty"
	ReportChangeWaiting  = "waiting"
)

// ReportMarkers returns the change markers of the platform's synced
// reports whose details have been read, by key: FetchReports reads details
// again only for reports whose marker moved.
func (s *Store) ReportMarkers(ctx context.Context, platformName string) (map[string]string, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT report_key, marker FROM my_reports
		WHERE platform = ? AND source = 'platform' AND detail_at IS NOT NULL`, platformName)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var k, m string
		if err := rows.Scan(&k, &m); err != nil {
			return nil, err
		}
		out[k] = m
	}
	return out, rows.Err()
}

// storedReport is what SaveReports compares a fetched report with.
type storedReport struct {
	id              int64
	state, severity string
	waitingOn       string
	edits           map[string]bool
	paymentsRead    bool
	detailed        bool // its details were read before
}

// SaveReports stores a batch of the platform's reports in one
// transaction and returns what changed. Fields the user set by hand keep
// their value; events and payments are replaced only when the fetch read
// them whole (Detailed, PaymentsKnown); the marker moves only with the
// details, so a report whose details weren't read this time is read next.
//
// Once the platform's reports have been listed before (its first listing
// is the account's past, not news), the changes are also kept for the
// notifier (my_report_changes), except a muted report's.
func (s *Store) SaveReports(ctx context.Context, platformName string, reports []platform.Report) ([]ReportChange, error) {
	var changes []ReportChange
	err := s.Write(ctx, func(tx *Tx) error {
		changes = changes[:0]
		var synced int
		if err := tx.tx.QueryRowContext(ctx, `SELECT count(*) FROM settings WHERE key = ?`,
			reportSettingKey(platformName, "synced_at")).Scan(&synced); err != nil {
			return err
		}
		for _, r := range reports {
			ch, err := tx.saveReport(ctx, platformName, r)
			if err != nil {
				return fmt.Errorf("report %s: %w", r.Key, err)
			}
			changes = append(changes, ch...)
			if synced == 0 {
				continue
			}
			for _, c := range ch {
				if _, err := tx.tx.ExecContext(ctx, `INSERT INTO my_report_changes (report_id, kind, from_value, to_value, amount,
					currency, occurred_at) SELECT id, ?, ?, ?, ?, ?, ? FROM my_reports WHERE id = ? AND NOT muted`,
					c.Kind, c.From, c.To, nullAmount(c), c.Currency, tx.now, c.ReportID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return changes, err
}

func nullAmount(c ReportChange) any {
	if c.Kind == ReportChangeBounty {
		return c.Amount
	}
	return nil
}

func (t *Tx) saveReport(ctx context.Context, platformName string, r platform.Report) ([]ReportChange, error) {
	if r.Key == "" || r.SubmittedAt.IsZero() {
		return nil, errors.New("no key or submission date")
	}
	if r.State == "" {
		return nil, errors.New("no state")
	}
	programID, err := t.reportProgram(ctx, platformName, r.ProgramHandle, r.ProgramName)
	if err != nil {
		return nil, err
	}
	old, err := t.storedReport(ctx, platformName, r.Key)
	if err != nil {
		return nil, err
	}
	raw := r.Raw
	if len(raw) == 0 || !json.Valid(raw) {
		raw = json.RawMessage("{}")
	}
	var detailAt any
	if r.Detailed {
		detailAt = t.now
	}
	var id int64
	if old == nil {
		err = t.tx.QueryRowContext(ctx, `INSERT INTO my_reports (platform, report_key, program_id, program_handle, program_name, title, url,
			state, raw_state, severity, severity_label, submitted_severity, severity_changed, cvss_score, submitted_at, first_response_at,
			triaged_at, first_bounty_at, closed_at, last_activity_at, last_program_activity_at, last_hunter_activity_at, waiting_on,
			comments, unread, duplicate_of, source, marker, detail_at, payments_at, raw, first_seen_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'platform',?,?,?,?,?,?) RETURNING id`,
			platformName, r.Key, programID, r.ProgramHandle, r.ProgramName, r.Title, r.URL, r.State, r.RawState, r.Severity,
			r.SeverityLabel, r.SubmittedSeverity, r.SeverityChanged, r.CVSS, ts(r.SubmittedAt), tsPtr(r.FirstResponseAt),
			tsPtr(r.TriagedAt), tsPtr(r.FirstBountyAt), tsPtr(r.ClosedAt), tsPtr(r.LastActivityAt), tsPtr(r.LastProgramActivityAt),
			tsPtr(r.LastHunterActivityAt), r.WaitingOn, r.Comments, r.Unread, r.DuplicateOf, markerIf(r), detailAt,
			paymentsAt(r, t.now), string(raw), t.now, t.now).Scan(&id)
		if err != nil {
			return nil, err
		}
	} else {
		id = old.id
		// A field set by hand keeps the hand's value: the CASE reads edits.
		_, err = t.tx.ExecContext(ctx, `UPDATE my_reports SET program_id = COALESCE(?2, program_id), program_handle = ?3,
			program_name = CASE WHEN json_extract(edits, '$.program_name') THEN program_name ELSE ?4 END,
			title = CASE WHEN json_extract(edits, '$.title') THEN title ELSE ?5 END,
			url = CASE WHEN json_extract(edits, '$.url') THEN url ELSE ?6 END,
			state = CASE WHEN json_extract(edits, '$.state') THEN state ELSE ?7 END, raw_state = ?8,
			severity = CASE WHEN json_extract(edits, '$.severity') THEN severity ELSE ?9 END, severity_label = ?10,
			submitted_severity = CASE WHEN json_extract(edits, '$.submitted_severity') THEN submitted_severity
				WHEN ?11 = '' AND NOT ?26 THEN submitted_severity ELSE ?11 END,
			severity_changed = CASE WHEN ?26 THEN ?12 ELSE MAX(severity_changed, ?12) END, cvss_score = COALESCE(?13, cvss_score),
			submitted_at = CASE WHEN json_extract(edits, '$.submitted_at') THEN submitted_at ELSE ?14 END,
			first_response_at = COALESCE(?15, first_response_at), triaged_at = COALESCE(?16, triaged_at),
			first_bounty_at = COALESCE(?17, first_bounty_at), closed_at = ?18, last_activity_at = COALESCE(?19, last_activity_at),
			last_program_activity_at = COALESCE(?20, last_program_activity_at),
			last_hunter_activity_at = COALESCE(?21, last_hunter_activity_at),
			waiting_on = CASE WHEN ?26 OR ?22 <> '' THEN ?22 ELSE waiting_on END,
			comments = CASE WHEN ?26 OR ?23 > 0 THEN ?23 ELSE comments END, unread = ?24,
			duplicate_of = CASE WHEN ?26 OR ?25 <> '' THEN ?25 ELSE duplicate_of END, marker = CASE WHEN ?26 THEN ?27 ELSE marker END, detail_at = COALESCE(?28, detail_at),
			raw = ?29, gone_at = NULL, updated_at = ?30, payments_at = CASE WHEN ?31 THEN ?30 ELSE payments_at END
			WHERE id = ?1`,
			id, programID, r.ProgramHandle, r.ProgramName, r.Title, r.URL, r.State, r.RawState, r.Severity, r.SeverityLabel,
			r.SubmittedSeverity, r.SeverityChanged, r.CVSS, ts(r.SubmittedAt), tsPtr(r.FirstResponseAt), tsPtr(r.TriagedAt),
			tsPtr(r.FirstBountyAt), tsPtr(r.ClosedAt), tsPtr(r.LastActivityAt), tsPtr(r.LastProgramActivityAt),
			tsPtr(r.LastHunterActivityAt), r.WaitingOn, r.Comments, r.Unread, r.DuplicateOf, r.Detailed, r.Marker, detailAt,
			string(raw), t.now, r.PaymentsKnown)
		if err != nil {
			return nil, err
		}
	}
	if r.Detailed {
		if _, err := t.tx.ExecContext(ctx, `DELETE FROM my_report_events WHERE report_id = ?`, id); err != nil {
			return nil, err
		}
		for _, e := range r.Events {
			if _, err := t.tx.ExecContext(ctx, `INSERT INTO my_report_events (report_id, at, kind, from_value, to_value)
				VALUES (?, ?, ?, ?, ?)`, id, ts(e.At), e.Kind, e.From, e.To); err != nil {
				return nil, err
			}
		}
	}
	var newPayments []platform.ReportPayment
	if r.PaymentsKnown {
		if newPayments, err = t.replacePayments(ctx, id, r.Payments); err != nil {
			return nil, err
		}
	}
	return reportChanges(id, platformName, programID, r, old, newPayments), nil
}

// paymentsAt is when a new report's payments were read: now if they were.
func paymentsAt(r platform.Report, now string) any {
	if r.PaymentsKnown {
		return now
	}
	return nil
}

// markerIf is the marker a new report is saved with: none until its
// details are read.
func markerIf(r platform.Report) string {
	if r.Detailed {
		return r.Marker
	}
	return ""
}

// reportChanges compares a fetched report with what was stored. A report
// seen for the first time is "new" alone: its whole past isn't news.
func reportChanges(id int64, platformName string, programID *int64, r platform.Report, old *storedReport,
	newPayments []platform.ReportPayment) []ReportChange {
	base := ReportChange{ReportID: id, Platform: platformName, ProgramID: programID, ProgramName: r.ProgramName, Title: r.Title, URL: r.URL}
	if old == nil {
		c := base
		c.Kind, c.To = ReportChangeNew, r.State
		return []ReportChange{c}
	}
	var out []ReportChange
	if old.state != r.State && !old.edits["state"] {
		c := base
		c.Kind, c.From, c.To = ReportChangeState, old.state, r.State
		out = append(out, c)
	}
	// A severity first seen with the details is the report's past too.
	if old.severity != r.Severity && r.Severity != "" && !old.edits["severity"] && (old.detailed || !r.Detailed) {
		c := base
		c.Kind, c.From, c.To = ReportChangeSeverity, old.severity, r.Severity
		out = append(out, c)
	}
	// The first time a report's payments are read (its details read for
	// the first time after a listing-only save) they're its past, not news.
	if old.paymentsRead {
		for _, p := range newPayments {
			c := base
			c.Kind, c.To, c.Amount, c.Currency = ReportChangeBounty, p.Kind, p.Amount, p.Currency
			out = append(out, c)
		}
	}
	// Who spoke last is known from the details: their first read is the
	// report's past, not news.
	if r.WaitingOn == "you" && old.waitingOn != "you" && (old.detailed || !r.Detailed) {
		c := base
		c.Kind = ReportChangeWaiting
		out = append(out, c)
	}
	return out
}

func (t *Tx) storedReport(ctx context.Context, platformName, key string) (*storedReport, error) {
	var o storedReport
	var edits string
	err := t.tx.QueryRowContext(ctx, `SELECT id, state, severity, waiting_on, edits, payments_at IS NOT NULL, detail_at IS NOT NULL
		FROM my_reports WHERE platform = ? AND report_key = ?`, platformName, key).
		Scan(&o.id, &o.state, &o.severity, &o.waitingOn, &edits, &o.paymentsRead, &o.detailed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	o.edits = parseEdits(edits)
	return &o, nil
}

func parseEdits(s string) map[string]bool {
	m := map[string]bool{}
	_ = json.Unmarshal([]byte(s), &m)
	return m
}

// reportProgram finds the stored program a report belongs to: by handle
// (case aside: Bugcrowd's links lower-case it), else by name if exactly
// one program of the platform has it. nil when HuntDeck doesn't have it.
func (t *Tx) reportProgram(ctx context.Context, platformName, handle, name string) (*int64, error) {
	var id int64
	if handle != "" {
		err := t.tx.QueryRowContext(ctx, `SELECT id FROM programs WHERE platform = ? AND handle = ? COLLATE NOCASE
			ORDER BY handle = ? DESC, removed_at IS NULL DESC LIMIT 1`, platformName, handle, handle).Scan(&id)
		if err == nil {
			return &id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	if name == "" {
		return nil, nil
	}
	rows, err := t.tx.QueryContext(ctx, `SELECT id FROM programs WHERE platform = ? AND name = ? COLLATE NOCASE LIMIT 2`, platformName, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil || len(ids) != 1 {
		return nil, err
	}
	return &ids[0], nil
}

// replacePayments makes the report's payments the given set and returns
// the ones it didn't have before (by ref, or by kind, amount, currency and
// day when the platform gives no ref).
func (t *Tx) replacePayments(ctx context.Context, reportID int64, ps []platform.ReportPayment) ([]platform.ReportPayment, error) {
	rows, err := t.tx.QueryContext(ctx, `SELECT ref, kind, amount, currency, awarded_at FROM my_report_payments WHERE report_id = ?`, reportID)
	if err != nil {
		return nil, err
	}
	had := map[string]bool{}
	for rows.Next() {
		var p platform.ReportPayment
		var at string
		if err := rows.Scan(&p.Ref, &p.Kind, &p.Amount, &p.Currency, &at); err != nil {
			_ = rows.Close()
			return nil, err
		}
		p.AwardedAt = parseTS(at)
		had[paymentKey(p)] = true
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := t.tx.ExecContext(ctx, `DELETE FROM my_report_payments WHERE report_id = ?`, reportID); err != nil {
		return nil, err
	}
	var added []platform.ReportPayment
	for _, p := range ps {
		if p.Currency == "" || p.AwardedAt.IsZero() {
			continue
		}
		if _, err := t.tx.ExecContext(ctx, `INSERT INTO my_report_payments (report_id, ref, kind, amount, currency, awarded_at, paid_at, status)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, reportID, p.Ref, p.Kind, p.Amount, strings.ToUpper(p.Currency), ts(p.AwardedAt),
			tsPtr(p.PaidAt), p.Status); err != nil {
			return nil, err
		}
		if !had[paymentKey(p)] {
			added = append(added, p)
		}
	}
	return added, nil
}

func paymentKey(p platform.ReportPayment) string {
	if p.Ref != "" {
		return "ref:" + p.Ref
	}
	return fmt.Sprintf("%s|%.2f|%s|%s", p.Kind, p.Amount, strings.ToUpper(p.Currency), p.AwardedAt.UTC().Format(time.DateOnly))
}

// MarkReportsGone records, after a complete listing, that the platform's
// synced reports not in seen are gone from it (a deleted draft, a
// withdrawn report). They're kept, never deleted; one seen again is back.
func (s *Store) MarkReportsGone(ctx context.Context, platformName string, seen []string) (int, error) {
	if len(seen) == 0 {
		return 0, nil // an empty "complete" listing is a platform problem, not a wipe
	}
	keys := slices.Clone(seen)
	b, _ := json.Marshal(keys)
	var n int64
	err := s.Write(ctx, func(tx *Tx) error {
		res, err := tx.tx.ExecContext(ctx, `UPDATE my_reports SET gone_at = ?1 WHERE platform = ?2 AND source = 'platform'
			AND gone_at IS NULL AND report_key NOT IN (SELECT value FROM json_each(?3))`, tx.now, platformName, string(b))
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return int(n), err
}

// Report sync settings, per platform: reports.<platform>.off ("1" when the
// user turned reading reports off) and reports.<platform>.synced_at (the
// last sync that listed them).
func reportSettingKey(platformName, what string) string {
	return "reports." + platformName + "." + what
}

// ReportSync says whether the platform's reports are read and when they
// last were.
func (s *Store) ReportSync(ctx context.Context, platformName string) (enabled bool, syncedAt *time.Time, err error) {
	m, err := s.Settings(ctx, "reports."+platformName+".")
	if err != nil {
		return false, nil, err
	}
	if v := m[reportSettingKey(platformName, "synced_at")]; v != "" {
		t := parseTS(v)
		syncedAt = &t
	}
	return m[reportSettingKey(platformName, "off")] != "1", syncedAt, nil
}

// SetReportsSynced records that the platform's reports were just listed.
func (s *Store) SetReportsSynced(ctx context.Context, platformName string) error {
	return s.Write(ctx, func(tx *Tx) error { return tx.PutSetting(ctx, reportSettingKey(platformName, "synced_at"), tx.now) })
}

// SetReportsEnabled turns reading the platform's reports on or off.
func (s *Store) SetReportsEnabled(ctx context.Context, platformName string, on bool) error {
	return s.Write(ctx, func(tx *Tx) error {
		if on {
			return tx.DeleteSetting(ctx, reportSettingKey(platformName, "off"))
		}
		return tx.PutSetting(ctx, reportSettingKey(platformName, "off"), "1")
	})
}

// QuietDaysSetting holds how many days without a word from the program
// make an undecided report "gone quiet" (Settings › Reports).
const QuietDaysSetting = "reports.quiet_days"

// MaxQuietDays bounds the setting.
const MaxQuietDays = 365

// ReportQuietDays returns the gone-quiet days set in Settings, or def.
func (s *Store) ReportQuietDays(ctx context.Context, def int) (int, error) {
	m, err := s.Settings(ctx, QuietDaysSetting)
	if err != nil {
		return def, err
	}
	if n, err := strconv.Atoi(m[QuietDaysSetting]); err == nil && n >= 1 && n <= MaxQuietDays {
		return n, nil
	}
	return def, nil
}

// SetReportQuietDays saves the gone-quiet days; 0 goes back to the default.
func (s *Store) SetReportQuietDays(ctx context.Context, days int) error {
	return s.Write(ctx, func(tx *Tx) error {
		if days <= 0 {
			return tx.DeleteSetting(ctx, QuietDaysSetting)
		}
		return tx.PutSetting(ctx, QuietDaysSetting, strconv.Itoa(min(days, MaxQuietDays)))
	})
}
