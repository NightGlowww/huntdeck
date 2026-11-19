package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// disclosureCursorKey names the setting holding the newest disclosure time
// read from a platform's all-programs feed, where the next poll resumes.
func disclosureCursorKey(platformName string) string {
	return "disclosures." + platformName + ".cursor"
}

// DisclosureCursor returns where the platform's disclosure feed was last
// read up to, or nil if it never was.
func (s *Store) DisclosureCursor(ctx context.Context, platformName string) (*time.Time, error) {
	key := disclosureCursorKey(platformName)
	m, err := s.Settings(ctx, key)
	if err != nil {
		return nil, err
	}
	v, ok := m[key]
	if !ok {
		return nil, nil
	}
	t, err := time.Parse(TimeFormat, v)
	if err != nil {
		return nil, nil //nolint:nilerr // a bad value just means reading the feed from the start
	}
	return &t, nil
}

// SetDisclosureCursor records where the platform's disclosure feed was read
// up to.
func (t *Tx) SetDisclosureCursor(ctx context.Context, platformName string, at time.Time) error {
	return t.PutSetting(ctx, disclosureCursorKey(platformName), ts(at))
}

// ProgramIDs returns the IDs of a platform's programs by handle, including
// removed ones.
func (s *Store) ProgramIDs(ctx context.Context, platformName string) (map[string]int64, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT handle, id FROM programs WHERE platform = ?`, platformName)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int64{}
	for rows.Next() {
		var h string
		var id int64
		if err := rows.Scan(&h, &id); err != nil {
			return nil, err
		}
		out[h] = id
	}
	return out, rows.Err()
}

// DisclosuresDue returns the platform's live programs whose disclosure
// history hasn't been fetched yet.
func (s *Store) DisclosuresDue(ctx context.Context, platformName string) ([]ProgramRef, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT id, platform, handle, name, url, private, state FROM programs
		WHERE platform = ? AND removed_at IS NULL AND disclosures_synced_at IS NULL
		ORDER BY id`, platformName)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ProgramRef
	for rows.Next() {
		var r ProgramRef
		if err := rows.Scan(&r.ID, &r.Platform, &r.Handle, &r.Name, &r.URL, &r.Private, &r.State); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveDisclosure stores one disclosed report against a program, updating it
// if already known, and reports whether it was new.
func (t *Tx) SaveDisclosure(ctx context.Context, programID int64, d platform.Disclosure) (bool, error) {
	var known int
	if err := t.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM disclosed_reports WHERE program_id = ? AND report_id = ?`,
		programID, d.ID).Scan(&known); err != nil {
		return false, err
	}
	cves := d.CVEIDs
	if cves == nil {
		cves = []string{}
	}
	cveJSON, err := json.Marshal(cves)
	if err != nil {
		return false, err
	}
	raw := string(d.Raw)
	if raw == "" {
		raw = "{}"
	}
	_, err = t.tx.ExecContext(ctx, `
		INSERT INTO disclosed_reports (program_id, report_id, title, severity, weakness, cve_ids, substate, bounty,
			submitted_at, disclosed_at, votes, url, reporter, summary, raw, first_seen_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (program_id, report_id) DO UPDATE SET
			title = excluded.title, severity = excluded.severity, weakness = excluded.weakness, cve_ids = excluded.cve_ids,
			substate = excluded.substate, bounty = excluded.bounty, submitted_at = excluded.submitted_at,
			disclosed_at = excluded.disclosed_at, votes = excluded.votes, url = excluded.url, reporter = excluded.reporter,
			summary = excluded.summary, raw = excluded.raw, updated_at = excluded.updated_at`,
		programID, d.ID, d.Title, d.Severity, d.Weakness, string(cveJSON), d.Substate, d.Bounty,
		tsPtr(d.SubmittedAt), ts(d.DisclosedAt), d.Votes, d.URL, d.Reporter, d.Summary, raw, t.now, t.now)
	return known == 0, err
}

// RefreshDisclosureCount recomputes a program's cached disclosed-report
// count.
func (t *Tx) RefreshDisclosureCount(ctx context.Context, programID int64) error {
	_, err := t.tx.ExecContext(ctx, `
		UPDATE programs SET n_disclosed = (SELECT COUNT(*) FROM disclosed_reports WHERE program_id = ?1) WHERE id = ?1`, programID)
	return err
}

// MarkDisclosuresSynced records that a program's disclosure history was
// fetched in full.
func (t *Tx) MarkDisclosuresSynced(ctx context.Context, programID int64) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE programs SET disclosures_synced_at = ? WHERE id = ?`, t.now, programID)
	return err
}

// DisclosureView is one disclosed report as stored.
type DisclosureView struct {
	ReportID    string
	Title       string
	Severity    string // "none", "low", "medium", "high", "critical" or ""
	Weakness    string
	CVEIDs      []string
	Substate    string
	Bounty      *float64
	SubmittedAt *time.Time
	DisclosedAt time.Time
	Votes       int
	URL         string
	Reporter    string
	Summary     string
}

// DisclosureSummary describes all of a program's disclosed reports.
type DisclosureSummary struct {
	Total      int
	BySeverity []NamedCount // most severe first; only severities present
	Weaknesses []NamedCount // most common first, at most topWeaknesses
	Paid       int          // reports with a public bounty
	Bounties   float64      // sum of public bounties
	Latest     *time.Time   // newest disclosure
}

// NamedCount is a label and how often it occurs.
type NamedCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

const topWeaknesses = 8

var severityOrder = []string{"critical", "high", "medium", "low", "none", ""}

// ProgramDisclosures returns a program's newest disclosed reports (at most
// limit) and a summary of all of them.
func (s *Store) ProgramDisclosures(ctx context.Context, programID int64, limit int) ([]DisclosureView, DisclosureSummary, error) {
	var sum DisclosureSummary
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT report_id, title, severity, weakness, cve_ids, substate, bounty, submitted_at, disclosed_at, votes, url, reporter, summary
		FROM disclosed_reports WHERE program_id = ? ORDER BY disclosed_at DESC, id DESC`, programID)
	if err != nil {
		return nil, sum, err
	}
	defer func() { _ = rows.Close() }()
	var out []DisclosureView
	sev := map[string]int{}
	weak := map[string]int{}
	for rows.Next() {
		var d DisclosureView
		var cves, disclosed string
		var bounty sql.NullFloat64
		var submitted sql.NullString
		if err := rows.Scan(&d.ReportID, &d.Title, &d.Severity, &d.Weakness, &cves, &d.Substate, &bounty, &submitted,
			&disclosed, &d.Votes, &d.URL, &d.Reporter, &d.Summary); err != nil {
			return nil, sum, err
		}
		_ = json.Unmarshal([]byte(cves), &d.CVEIDs)
		d.Bounty, d.SubmittedAt, d.DisclosedAt = floatPtr(bounty), parseTSPtr(submitted), parseTS(disclosed)

		sum.Total++
		if sum.Latest == nil {
			latest := d.DisclosedAt
			sum.Latest = &latest
		}
		sev[d.Severity]++
		if d.Weakness != "" {
			weak[d.Weakness]++
		}
		if d.Bounty != nil && *d.Bounty > 0 {
			sum.Paid++
			sum.Bounties += *d.Bounty
		}
		if len(out) < limit {
			out = append(out, d)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, sum, err
	}
	for _, name := range severityOrder {
		if n := sev[name]; n > 0 {
			sum.BySeverity = append(sum.BySeverity, NamedCount{name, n})
		}
	}
	for name, n := range weak {
		sum.Weaknesses = append(sum.Weaknesses, NamedCount{name, n})
	}
	sort.Slice(sum.Weaknesses, func(i, j int) bool {
		a, b := sum.Weaknesses[i], sum.Weaknesses[j]
		return a.Count > b.Count || (a.Count == b.Count && a.Name < b.Name)
	})
	if len(sum.Weaknesses) > topWeaknesses {
		sum.Weaknesses = sum.Weaknesses[:topWeaknesses]
	}
	return out, sum, nil
}

// DisclosureFilter selects disclosed reports across programs.
type DisclosureFilter struct {
	Platform string
	Query    string   // matches the title, program name or handle
	Severity []string // any of these; empty for all
	Weakness string   // exact weakness name
	PaidOnly bool     // only reports with a public bounty
	WithCVE  bool     // only reports that name a CVE
	Since    time.Time
	Sort     string // "new" (default), "bounty" or "votes"
	Limit    int
	Offset   int
}

// where is the WHERE clause (or "") for f, on disclosed_reports d and
// programs p, with its arguments.
func (f DisclosureFilter) where() (string, []any) {
	var conds []string
	var args []any
	if f.Platform != "" {
		conds = append(conds, "p.platform = ?")
		args = append(args, f.Platform)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		conds = append(conds, `(lower(d.title) LIKE ? ESCAPE '\' OR lower(p.name) LIKE ? ESCAPE '\' OR lower(p.handle) LIKE ? ESCAPE '\')`)
		args = append(args, likeEscape(q), likeEscape(q), likeEscape(q))
	}
	if len(f.Severity) > 0 {
		conds = append(conds, "d.severity IN ("+placeholders(len(f.Severity))+")")
		for _, v := range f.Severity {
			args = append(args, v)
		}
	}
	if f.Weakness != "" {
		conds = append(conds, "d.weakness = ?")
		args = append(args, f.Weakness)
	}
	if f.PaidOnly {
		conds = append(conds, "d.bounty > 0")
	}
	if f.WithCVE {
		conds = append(conds, "d.cve_ids IS NOT NULL AND d.cve_ids NOT IN ('', '[]')")
	}
	if !f.Since.IsZero() {
		conds = append(conds, "d.disclosed_at >= ?")
		args = append(args, ts(f.Since))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	return where, args
}

// DisclosureRow is a disclosed report with its program.
type DisclosureRow struct {
	DisclosureView
	Program  ProgramRef
	Currency string // the program's
}

// Disclosures returns one page of disclosed reports matching f and the total.
func (s *Store) Disclosures(ctx context.Context, f DisclosureFilter) ([]DisclosureRow, int, error) {
	where, args := f.where()
	from := ` FROM disclosed_reports d JOIN programs p ON p.id = d.program_id` + where

	var total int
	if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*)`+from, args...).Scan(&total); err != nil { //nolint:gosec // constant fragments; values are bound parameters
		return nil, 0, err
	}
	order := "d.disclosed_at DESC, d.id DESC"
	switch f.Sort {
	case "bounty":
		order = "d.bounty IS NULL, d.bounty DESC, d.disclosed_at DESC"
	case "votes":
		order = "d.votes DESC, d.disclosed_at DESC"
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	//nolint:gosec // constant fragments; values are bound parameters
	q := `SELECT d.report_id, d.title, d.severity, d.weakness, d.cve_ids, d.substate, d.bounty, d.submitted_at, d.disclosed_at,
			d.votes, d.url, d.reporter, d.summary, p.id, p.platform, p.handle, p.name, p.url, p.private, p.state, p.currency` +
		from + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?`
	rows, err := s.db.R.QueryContext(ctx, q, append(args, limit, max(0, f.Offset))...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var out []DisclosureRow
	for rows.Next() {
		var r DisclosureRow
		d, p := &r.DisclosureView, &r.Program
		var cves, disclosed string
		var bounty sql.NullFloat64
		var submitted sql.NullString
		if err := rows.Scan(&d.ReportID, &d.Title, &d.Severity, &d.Weakness, &cves, &d.Substate, &bounty, &submitted, &disclosed,
			&d.Votes, &d.URL, &d.Reporter, &d.Summary, &p.ID, &p.Platform, &p.Handle, &p.Name, &p.URL, &p.Private, &p.State,
			&r.Currency); err != nil {
			return nil, 0, err
		}
		_ = json.Unmarshal([]byte(cves), &d.CVEIDs)
		d.Bounty, d.SubmittedAt, d.DisclosedAt = floatPtr(bounty), parseTSPtr(submitted), parseTS(disclosed)
		out = append(out, r)
	}
	return out, total, rows.Err()
}
