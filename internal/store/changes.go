package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// A program whose state changes FlipFlops or more times within FlipWindow
// is flip-flopping: Intigriti pauses programs when they reach their report
// limit and reopens them as the queue clears, often several times a day.
// Those changes are true but say little, so lists collapse them, "back
// open" leaves them out, and notifications wait for a pause that lasts
// (see notify).
const (
	FlipFlops  = 3
	FlipWindow = 7 * 24 * time.Hour
)

// flipsAround counts a program's state changes within FlipWindow either
// side of change c, and flipsUpTo those in the FlipWindow up to and
// including it (all a notification can know of), for SQL. resumedAt
// compares the first with FlipFlops; both are written out so the SQL stays
// constant (TestFlipSQL checks them).
const (
	flipsAround = `(SELECT COUNT(*) FROM changes f WHERE f.program_id = c.program_id AND f.kind = 'state_changed'
	AND f.occurred_at > strftime('%Y-%m-%dT%H:%M:%fZ', c.occurred_at, '-7 days')
	AND f.occurred_at <= strftime('%Y-%m-%dT%H:%M:%fZ', c.occurred_at, '+7 days'))`
	flipsUpTo = `(SELECT COUNT(*) FROM changes f WHERE f.program_id = c.program_id AND f.kind = 'state_changed'
	AND f.occurred_at > strftime('%Y-%m-%dT%H:%M:%fZ', c.occurred_at, '-7 days') AND f.occurred_at <= c.occurred_at)`
)

// flipWeeks numbers each state change since ?2 (a time, or "" for all)
// within its program's week (weeks counted back from ?1, a time): n is
// how many the program had that week, rn its place, newest first. A list
// collapses a week of FlipFlops or more into its newest. A list of changes
// since a time needs those from FlipWindow before it (flipsFrom): the
// week it starts in may begin that long before.
var flipWeeks = fmt.Sprintf(`flips AS (SELECT id, COUNT(*) OVER w AS n, ROW_NUMBER() OVER (w ORDER BY occurred_at DESC, id DESC) AS rn
	FROM (SELECT id, program_id, occurred_at, CAST((julianday(?1) - julianday(occurred_at)) / %d AS INTEGER) AS week
		FROM changes WHERE kind = '%s' AND occurred_at >= ?2)
	WINDOW w AS (PARTITION BY program_id, week))`, int(FlipWindow.Hours()/24), ChangeStateChanged)

// flipsFrom is flipWeeks' ?2 for a list of changes since since.
func flipsFrom(since time.Time) string {
	if since.IsZero() {
		return ""
	}
	return ts(since.Add(-FlipWindow))
}

// ChangeFilter selects changes.
// Change orders besides by date (ChangeFilter.Order).
const (
	ChangesByName     = "name"
	ChangesByPlatform = "platform"
)

type ChangeFilter struct {
	ID        int64 // just this change
	Since     time.Time
	Kinds     []string
	NotKinds  []string // leave these kinds out
	Platform  string
	ProgramID int64
	Watch     []ProgramKey // keeps to these programs' changes, if any
	// NewValues, if any, keeps changes whose new value is one of these: for
	// state changes, the state a program went to.
	NewValues []string
	// Order is "" (by date: newest first with Newest), ChangesByName (the
	// program's name, A to Z) or ChangesByPlatform (then newest first);
	// Reverse turns the name or platform order around.
	Order   string
	Reverse bool
	// BountyOnly keeps changes to programs that pay bounties, NoBounty
	// those to programs that don't (VDPs), as ProgramFilter's do.
	BountyOnly bool
	NoBounty   bool
	Newest     bool // newest first (default oldest first)
	// CollapseFlips shows a flip-flopping program's state changes (see
	// FlipFlops) as one per week, its newest, with Flips set.
	CollapseFlips bool
	Limit         int
	Offset        int
}

// Changes returns one page of changes and the total count.
func (s *Store) Changes(ctx context.Context, f ChangeFilter) ([]ChangeView, int, error) {
	conds := []string{"c.occurred_at >= ?"}
	args := []any{ts(f.Since)}
	with, join, flips := "", "", "0"
	if f.CollapseFlips {
		flipSQL := flipWeeks
		flipArgs := []any{ts(s.now()), flipsFrom(f.Since)}
		if f.ProgramID != 0 {
			// A program page's feed: only that program's weeks matter, so the
			// window doesn't number every other program's state changes.
			flipSQL = strings.Replace(flipWeeks, "AND occurred_at >= ?2)", "AND occurred_at >= ?2 AND program_id = ?3)", 1)
			flipArgs = append(flipArgs, f.ProgramID)
		}
		with = "WITH " + flipSQL + " "
		join = " LEFT JOIN flips fw ON fw.id = c.id"
		conds = append(conds, fmt.Sprintf("(fw.id IS NULL OR fw.n < %d OR fw.rn = 1)", FlipFlops))
		flips = fmt.Sprintf("CASE WHEN fw.n >= %d THEN fw.n ELSE 0 END", FlipFlops)
		args = append(flipArgs, args...)
	}
	if len(f.Kinds) > 0 {
		conds = append(conds, "c.kind IN ("+placeholders(len(f.Kinds))+")")
		for _, k := range f.Kinds {
			args = append(args, k)
		}
	}
	if len(f.NotKinds) > 0 {
		conds = append(conds, "c.kind NOT IN ("+placeholders(len(f.NotKinds))+")")
		for _, k := range f.NotKinds {
			args = append(args, k)
		}
	}
	if f.Platform != "" {
		conds = append(conds, "p.platform = ?")
		args = append(args, f.Platform)
	}
	if f.BountyOnly {
		conds = append(conds, "p.offers_bounty = 1")
	}
	if f.NoBounty {
		conds = append(conds, "p.offers_bounty = 0")
	}
	if len(f.Watch) > 0 {
		cond, wargs := watchCond(f.Watch)
		conds, args = append(conds, cond), append(args, wargs...)
	}
	if len(f.NewValues) > 0 {
		conds = append(conds, "c.new_value IN ("+placeholders(len(f.NewValues))+")")
		for _, v := range f.NewValues {
			args = append(args, v)
		}
	}
	if f.ProgramID != 0 {
		conds = append(conds, "c.program_id = ?")
		args = append(args, f.ProgramID)
	}
	if f.ID != 0 {
		conds = append(conds, "c.id = ?")
		args = append(args, f.ID)
	}
	from := ` FROM changes c JOIN programs p ON p.id = c.program_id` + join + ` WHERE ` + strings.Join(conds, " AND ")

	var total int
	if err := s.db.R.QueryRowContext(ctx, with+`SELECT COUNT(*)`+from, args...).Scan(&total); err != nil { //nolint:gosec // constant fragments; values are bound parameters
		return nil, 0, err
	}
	order := "c.occurred_at, c.id"
	if f.Newest {
		order = "c.occurred_at DESC, c.id DESC"
	}
	dir := ""
	if f.Reverse {
		dir = " DESC"
	}
	switch f.Order {
	case ChangesByName:
		order = "lower(p.name)" + dir + ", p.id, " + order
	case ChangesByPlatform:
		order = "p.platform" + dir + ", " + order
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	sel := with + `SELECT c.id, c.occurred_at, COALESCE(c.poll_run_id, 0), c.program_id, COALESCE(c.scope_entry_id, 0), c.kind,
			c.field, c.old_value, c.new_value, p.platform, p.handle, p.name, p.url, p.private, p.state, p.currency,
			COALESCE((SELECT se.identifier FROM scope_entries se WHERE se.id = c.scope_entry_id), ''), ` + flips
	q := sel + from + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?` //nolint:gosec // constant fragments; values are bound parameters
	rows, err := s.db.R.QueryContext(ctx, q, append(args, limit, max(0, f.Offset))...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var out []ChangeView
	for rows.Next() {
		var v ChangeView
		var occurred string
		if err := rows.Scan(&v.ID, &occurred, &v.PollRunID, &v.ProgramID, &v.ScopeEntryID, &v.Kind, &v.Field,
			&v.OldValue, &v.NewValue, &v.Program.Platform, &v.Program.Handle, &v.Program.Name, &v.Program.URL,
			&v.Program.Private, &v.Program.State, &v.Currency, &v.Asset, &v.Flips); err != nil {
			return nil, 0, err
		}
		v.OccurredAt = parseTS(occurred)
		v.Program.ID = v.ProgramID
		out = append(out, v)
	}
	return out, total, rows.Err()
}

// Change kinds.
const (
	ChangeProgramAdded      = "program_added"
	ChangeProgramRemoved    = "program_removed"
	ChangeVisibilityChanged = "visibility_changed"
	ChangeStateChanged      = "state_changed"
	ChangeBountyChanged     = "bounty_changed"
	ChangeProgramRenamed    = "program_renamed"
	ChangeScopeAdded        = "scope_added"
	ChangeScopeRemoved      = "scope_removed"
	ChangeScopeUpdated      = "scope_updated"
	// ChangePolicyChanged is an edit to a program's rules text; Field says
	// which: "policy", "campaign" (reward notes) or "exclusions".
	ChangePolicyChanged = "policy_changed"
	// ChangeRewardsChanged is a new bounty table; the values are
	// platform.RewardRow lists as JSON.
	ChangeRewardsChanged = "rewards_changed"
	// ChangeAnnouncement is a program's post to researchers, dated when it
	// was published: Field is its ID, NewValue its title, then a blank line
	// and its text (Markdown).
	ChangeAnnouncement = "announcement"
)

// ChangeKinds are every change kind.
func ChangeKinds() []string {
	return []string{ChangeProgramAdded, ChangeProgramRemoved, ChangeVisibilityChanged, ChangeStateChanged, ChangeBountyChanged,
		ChangeProgramRenamed, ChangeScopeAdded, ChangeScopeRemoved, ChangeScopeUpdated, ChangePolicyChanged, ChangeRewardsChanged,
		ChangeAnnouncement}
}

// Fields of ChangePolicyChanged.
const (
	FieldPolicy     = "policy"
	FieldCampaign   = "campaign"
	FieldExclusions = "exclusions"
	FieldInScope    = "brief_in_scope" // not "in_scope", a scope entry's field
	FieldOutScope   = "brief_out_of_scope"
	FieldSeverity   = "brief_severity"
	FieldFAQ        = "brief_faq"
)

// Change is one row of the change log.
type Change struct {
	ID           int64
	OccurredAt   time.Time
	PollRunID    int64
	ProgramID    int64
	ScopeEntryID int64 // 0 when the change is about the program
	Kind         string
	Field        string
	OldValue     string
	NewValue     string
}

// AddChange appends to the change log.
func (t *Tx) AddChange(ctx context.Context, c Change) error {
	var entry, run any
	if c.ScopeEntryID != 0 {
		entry = c.ScopeEntryID
	}
	if c.PollRunID != 0 { // stats refreshed outside a poll
		run = c.PollRunID
	}
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO changes (occurred_at, poll_run_id, program_id, scope_entry_id, kind, field, old_value, new_value)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.now, run, c.ProgramID, entry, c.Kind, c.Field, c.OldValue, c.NewValue)
	return err
}
