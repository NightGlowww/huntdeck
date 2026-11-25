package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/scope"
)

// TargetFilter selects targets for export.
type TargetFilter struct {
	Kind          string // one of scope.Kinds, or scope.KindRoot
	OutOfScope    bool   // export out-of-scope targets instead of in-scope ones
	Platform      string // empty for all
	BountyOnly    bool   // only bounty-eligible entries on programs that pay
	PrivateOnly   bool
	IncludePaused bool // include paused/closed programs
	// IncludeIgnored includes programs the user ignores.
	IncludeIgnored bool
	Tag            string // only programs with this tag
	// AddedSince, if set, keeps targets of entries added to their program
	// since then (scope_entries.added_at; entries of unknown age, or only
	// known to be older than a date, never match).
	AddedSince time.Time
	// ChangedSince, if set, keeps targets of entries added or edited
	// after then (scope_entries.updated_at: when HuntDeck saw the entry
	// appear or its content change).
	ChangedSince time.Time
	// Order is how BestTargets sorts: TargetsByScore (the default),
	// TargetsByNewest or TargetsByBounty; TargetRows takes the last two
	// (its default is by value). Reverse turns TargetsByBounty and
	// BestTargets' orders around; targets without a bounty stay last either
	// way.
	Order   string
	Reverse bool
	// JoinedPerProgram, if set, keeps BestTargets to that many of each
	// program's targets new to the user (TargetRow.Joined), its best ones,
	// so a large program just joined doesn't fill a short list.
	JoinedPerProgram int
	// OnePerProgram keeps the first target of each program in the sorted list, so
	// a program with eight hundred wildcards does not fill the page: the reader
	// sees which programs have something, then filters to one.
	OnePerProgram bool
	// Limit and Offset page EachTarget and EachBestTarget (Limit 0: every
	// one from Offset on).
	Limit, Offset int
	// Query keeps EachTarget's and EachBestTarget's targets to those
	// containing it, ignoring case (TargetRows and BestTargets take theirs
	// as an argument).
	Query string
	// AssetTypes, if any, lists scope entries of these asset types instead
	// of targets: apps, hardware and the like, which have none (assets.go).
	AssetTypes []string
	// Kinds, if any, keeps BestTargets to these kinds (KindApp for Android
	// and iOS apps) in place of Kind: the hunt board's kind chips.
	Kinds []string
	// Exclude leaves out targets whose value, or whose program's name or
	// handle, contains any of these, ignoring case.
	Exclude []string
}

// excludeCond is Exclude's condition for value (a column), and its
// arguments; "" for none.
func excludeCond(terms []string, value string) (string, []any) {
	var parts []string
	var args []any
	for _, t := range terms {
		if t = strings.TrimSpace(t); t == "" {
			continue
		}
		parts = append(parts, "lower("+value+") LIKE ? ESCAPE '\\' OR lower(p.name) LIKE ? ESCAPE '\\' OR lower(p.handle) LIKE ? ESCAPE '\\'")
		e := likeEscape(t)
		args = append(args, e, e, e)
	}
	if len(parts) == 0 {
		return "", nil
	}
	return "NOT (" + strings.Join(parts, " OR ") + ")", args
}

// page is the LIMIT clause for f's Limit and Offset, and its arguments.
func (f TargetFilter) page() (string, []any) {
	if f.Limit <= 0 && f.Offset <= 0 {
		return "", nil
	}
	limit := f.Limit
	if limit <= 0 {
		limit = -1 // SQLite: no limit
	}
	return ` LIMIT ? OFFSET ?`, []any{limit, max(f.Offset, 0)}
}

// Orders for BestTargets.
const (
	TargetsByScore  = ""       // best first
	TargetsByNewest = "new"    // newest first, then best
	TargetsByBounty = "bounty" // highest bounty first (the top of the range), then best
)

// EachTarget calls fn with each distinct target value matching f, in sorted
// order, streaming from the database so exports of any size use little memory.
// It stops at the first error fn returns.
func (s *Store) EachTarget(ctx context.Context, f TargetFilter, fn func(value string) error) error {
	if len(f.AssetTypes) > 0 {
		return s.eachAsset(ctx, f, fn)
	}
	k := kindOf(f.Kind)
	//nolint:gosec // constant fragments; values are bound parameters
	q := `
		SELECT DISTINCT ` + k.value + ` FROM scope_targets t
		JOIN scope_entries e ON e.id = t.scope_entry_id
		JOIN programs p ON p.id = e.program_id
		WHERE ` + k.cond + ` AND e.in_scope = ? AND e.removed_at IS NULL AND p.removed_at IS NULL`
	args := append(k.args, b2i(!f.OutOfScope))
	if f.Platform != "" {
		q += ` AND p.platform = ?`
		args = append(args, f.Platform)
	}
	if f.BountyOnly {
		q += ` AND e.bounty_eligible = 1 AND p.offers_bounty = 1`
	}
	if f.PrivateOnly {
		q += ` AND p.private = 1`
	}
	if !f.IncludePaused {
		q += ` AND p.state = 'open'`
	}
	if !f.IncludeIgnored {
		q += ` AND p.ignored = 0`
	}
	if f.Tag != "" {
		q += ` AND ` + tagCond
		args = append(args, f.Tag)
	}
	if !f.AddedSince.IsZero() {
		q += ` AND e.added_at >= ? AND e.added_bound = 0`
		args = append(args, ts(f.AddedSince))
	}
	if !f.ChangedSince.IsZero() {
		q += ` AND e.updated_at > ?`
		args = append(args, ts(f.ChangedSince))
	}
	if query := strings.TrimSpace(f.Query); query != "" {
		q += ` AND lower(` + k.value + `) LIKE ? ESCAPE '\'`
		args = append(args, likeEscape(query))
	}
	if c, cargs := excludeCond(f.Exclude, k.value); c != "" {
		q += " AND " + c
		args = append(args, cargs...)
	}
	q += ` ORDER BY ` + k.value
	lim, largs := f.page()
	q += lim
	args = append(args, largs...)

	rows, err := s.db.R.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return err
		}
		if err := fn(v); err != nil {
			return err
		}
	}
	return rows.Err()
}

// targetKind is how queries select one kind of target: value is the column
// holding the exported value, cond restricts rows to the kind, and same(a)
// says that target alias a has the same value as t. ordered, if set, is an
// INDEXED BY clause for t that walks cond's rows in value order (and counts
// them without reading the table).
type targetKind struct {
	value, cond string
	args        []any
	same        func(alias string) string
	ordered     string
}

// webKinds are the kinds with root domains.
const webKinds = `('wildcard', 'domain', 'url')`

// allKinds are every kind of target (scope.Kinds), for a list of all.
const allKinds = `('wildcard', 'domain', 'url', 'ip', 'cidr')`

// appKinds are the asset types scored as apps (scope.KindApp).
const appKinds = `('android', 'ios')`

func kindOf(kind string) targetKind {
	if kind == scope.KindRoot {
		// cond matches scope_targets_root's WHERE term for term, and same
		// scope_targets_program_root's, so a program's other targets under
		// the apex are one index seek away.
		return targetKind{value: "t.apex", cond: "t.kind IN " + webKinds + " AND t.apex <> ''",
			same: func(a string) string {
				return a + ".program_id = t.program_id AND " + a + ".kind IN " + webKinds + " AND " + a + ".apex <> '' AND " + a + ".apex = t.apex"
			},
			ordered: " INDEXED BY scope_targets_root"}
	}
	if kind == "" { // every kind of target (not roots, which are derived)
		return targetKind{value: "t.value", cond: "t.kind IN " + allKinds,
			same: func(a string) string {
				return a + ".program_id = t.program_id AND " + a + ".kind = t.kind AND " + a + ".value = t.value"
			}}
	}
	// same seeks scope_targets_program_value.
	return targetKind{value: "t.value", cond: "t.kind = ?", args: []any{kind},
		same: func(a string) string {
			return a + ".program_id = t.program_id AND " + a + ".kind = t.kind AND " + a + ".value = t.value"
		}}
}

// ProgramRef identifies a program for display.
type ProgramRef struct {
	ID       int64
	Platform string
	Handle   string
	Name     string
	URL      string
	Private  bool
	State    string
}

// MatchRules loads the live targets that could match input (see
// scope.Candidates) as matching rules, plus the programs they belong to.
func (s *Store) MatchRules(ctx context.Context, input string) ([]scope.Rule, map[int64]ProgramRef, error) {
	hosts, cidrs, ok := scope.Candidates(input)
	if !ok {
		return nil, map[int64]ProgramRef{}, nil
	}
	args := make([]any, 0, len(hosts))
	for _, h := range hosts {
		args = append(args, h)
	}
	// host <> '' lets the host lookup use its (partial) index; an IP also
	// reads every range, through the kind index.
	cond := `t.host <> '' AND t.host IN (` + placeholders(len(hosts)) + `)`
	if cidrs {
		cond = `((` + cond + `) OR t.kind = 'cidr')`
	}
	progs := map[int64]ProgramRef{}
	rules, err := s.queryRules(ctx, cond, args, progs)
	return rules, progs, err
}

// BulkRules holds the live rules that could match a batch of inputs.
type BulkRules struct {
	byHost   map[string][]scope.Rule
	cidrs    []cidrRule
	Programs map[int64]ProgramRef
}

// cidrRule is a range rule with its range parsed once, not once per IP.
type cidrRule struct {
	rule   scope.Rule
	prefix netip.Prefix
}

// For returns the rules that could match input, for scope.Match: an IP
// gets only the ranges holding it.
func (b *BulkRules) For(input string) []scope.Rule {
	hosts, isIP, ok := scope.Candidates(input)
	if !ok {
		return nil
	}
	var out []scope.Rule
	for _, h := range hosts {
		out = append(out, b.byHost[h]...)
	}
	if isIP {
		if addr, err := netip.ParseAddr(hosts[0]); err == nil {
			for _, c := range b.cidrs {
				if c.prefix.Contains(addr) {
					out = append(out, c.rule)
				}
			}
		}
	}
	return out
}

// MaxBulkLookups is the most distinct hosts one MatchRulesBulk looks up:
// a large subdomain list shares its parents, so stays well under it.
const MaxBulkLookups = 60000

// ErrTooManyLookups is MatchRulesBulk's answer to a batch past
// MaxBulkLookups.
var ErrTooManyLookups = errors.New("too many distinct names to look up")

// matchBatch is how many hosts one bulk lookup query asks for.
const matchBatch = 500

// MatchRulesBulk loads the rules for many inputs at once (hosts, IPs or
// URLs): their candidate hosts in batches, and every range once if any
// input is an IP.
func (s *Store) MatchRulesBulk(ctx context.Context, inputs []string) (*BulkRules, error) {
	b := &BulkRules{byHost: map[string][]scope.Rule{}, Programs: map[int64]ProgramRef{}}
	seen := map[string]bool{}
	var hosts []string
	anyIP := false
	for _, in := range inputs {
		cand, isIP, ok := scope.Candidates(in)
		if !ok {
			continue
		}
		anyIP = anyIP || isIP
		for _, h := range cand {
			if !seen[h] {
				seen[h] = true
				hosts = append(hosts, h)
			}
		}
		if len(hosts) > MaxBulkLookups {
			return nil, ErrTooManyLookups
		}
	}
	for start := 0; start < len(hosts); start += matchBatch {
		batch := hosts[start:min(start+matchBatch, len(hosts))]
		args := make([]any, len(batch))
		for i, h := range batch {
			args[i] = h
		}
		rules, err := s.queryRules(ctx, `t.host <> '' AND t.host IN (`+placeholders(len(batch))+`)`, args, b.Programs)
		if err != nil {
			return nil, err
		}
		for _, r := range rules {
			b.byHost[r.Target.Host] = append(b.byHost[r.Target.Host], r)
		}
	}
	if anyIP {
		cidrs, err := s.queryRules(ctx, `t.kind = 'cidr'`, nil, b.Programs)
		if err != nil {
			return nil, err
		}
		for _, r := range cidrs {
			if p, err := netip.ParsePrefix(r.Target.Value); err == nil {
				b.cidrs = append(b.cidrs, cidrRule{rule: r, prefix: p})
			}
		}
	}
	return b, nil
}

// queryRules runs the match query with cond, adding the programs it sees
// to progs.
func (s *Store) queryRules(ctx context.Context, cond string, args []any, progs map[int64]ProgramRef) ([]scope.Rule, error) {
	rows, err := s.db.R.QueryContext(ctx, matchHead+cond, args...) //nolint:gosec // only placeholders are concatenated; values are bound
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var rules []scope.Rule
	for rows.Next() {
		var p ProgramRef
		var r scope.Rule
		if err := rows.Scan(&p.ID, &p.Platform, &p.Handle, &p.Name, &p.URL, &p.Private, &p.State, &r.InScope,
			&r.Target.Kind, &r.Target.Value, &r.Target.Host, &r.Target.Apex); err != nil {
			return nil, err
		}
		r.ProgramID = p.ID
		rules = append(rules, r)
		progs[p.ID] = p
	}
	return rules, rows.Err()
}

const matchHead = `
		SELECT p.id, p.platform, p.handle, p.name, p.url, p.private, p.state, e.in_scope, t.kind, t.value, t.host, t.apex
		FROM scope_targets t
		JOIN scope_entries e ON e.id = t.scope_entry_id
		JOIN programs p ON p.id = e.program_id
		WHERE e.removed_at IS NULL AND p.removed_at IS NULL AND `

// PastEntry is a scope entry a program no longer lists: removed from its
// scope, or belonging to a program that has gone.
type PastEntry struct {
	Program    ProgramRef
	InScope    bool
	AssetType  string
	Identifier string
	RemovedAt  time.Time
}

// pastEntriesMax bounds how many removed entries one lookup considers.
const pastEntriesMax = 2000

// PastEntries returns removed scope entries that could match input (a host,
// IP or URL): those whose identifier mentions its root domain, plus, for
// IPs, any that could be a range. Callers parse and match them; most recent
// removals first.
func (s *Store) PastEntries(ctx context.Context, input string) ([]PastEntry, error) {
	hosts, isIP, ok := scope.Candidates(input)
	if !ok {
		return nil, nil
	}
	key := hosts[0]
	if root := scope.RootDomain(key); root != "" {
		key = root
	}
	cond := `lower(e.identifier) LIKE ?1 ESCAPE '\'`
	if isIP {
		cond = `(` + cond + ` OR (e.asset_type IN ('cidr', 'ip', 'other') AND e.identifier LIKE '%/%'))`
	}
	// Removed entries come from their own index (scope_entries_removed);
	// a removed program's live entries from its program_id.
	const cols = `SELECT p.id, p.platform, p.handle, p.name, p.url, p.private, p.state, e.in_scope, e.asset_type, e.identifier`
	//nolint:gosec // constant fragments; values are bound parameters
	rows, err := s.db.R.QueryContext(ctx, cols+`, e.removed_at AS gone, e.id AS eid
		FROM scope_entries e JOIN programs p ON p.id = e.program_id
		WHERE e.removed_at IS NOT NULL AND `+cond+`
		UNION ALL `+cols+`, p.removed_at, e.id
		FROM programs p CROSS JOIN scope_entries e ON e.program_id = p.id
		WHERE p.removed_at IS NOT NULL AND e.removed_at IS NULL AND `+cond+`
		ORDER BY gone DESC, eid LIMIT ?2`, likeEscape(key), pastEntriesMax)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PastEntry
	for rows.Next() {
		var e PastEntry
		var gone string
		var eid int64
		p := &e.Program
		if err := rows.Scan(&p.ID, &p.Platform, &p.Handle, &p.Name, &p.URL, &p.Private, &p.State, &e.InScope,
			&e.AssetType, &e.Identifier, &gone, &eid); err != nil {
			return nil, err
		}
		e.RemovedAt = parseTS(gone)
		out = append(out, e)
	}
	return out, rows.Err()
}

// PlatformSummary counts what is stored for one platform, as the pages
// the hunt board links to count it: ignored programs left out, and
// everything but Programs, Private and Removed for open programs only
// (the Programs list's and Targets page's default).
type PlatformSummary struct {
	Platform        string
	Programs        int // listed, paused and closed ones too
	Private         int // listed, paused and closed ones too
	Open            int
	Bounty          int // open programs that pay bounties
	Removed         int
	InScopeEntries  int
	OutScopeEntries int
	Targets         map[string]int // by kind: in-scope, live entries, counted once per program
}

// Summary returns per-platform counts.
func (s *Store) Summary(ctx context.Context) ([]PlatformSummary, error) {
	// The cached per-program target counts are the Targets page's: one
	// per program and value (RefreshScopeCounts).
	const open = `removed_at IS NULL AND ignored = 0 AND state = 'open'`
	//nolint:gosec // constant fragments
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT platform,
			SUM(removed_at IS NULL AND ignored = 0), SUM(removed_at IS NULL AND ignored = 0 AND private = 1), SUM(`+open+`),
			SUM(`+open+` AND offers_bounty = 1), SUM(removed_at IS NOT NULL),
			SUM(CASE WHEN `+open+` THEN n_in_scope END), SUM(CASE WHEN `+open+` THEN n_out_scope END),
			SUM(CASE WHEN `+open+` THEN n_wildcards END), SUM(CASE WHEN `+open+` THEN n_domains END),
			SUM(CASE WHEN `+open+` THEN n_urls END), SUM(CASE WHEN `+open+` THEN n_ips END),
			SUM(CASE WHEN `+open+` THEN n_cidrs END)
		FROM programs GROUP BY platform ORDER BY platform`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PlatformSummary
	for rows.Next() {
		var ps PlatformSummary
		var in, outScope, wc, dom, urls, ips, cidrs sql.NullInt64
		if err := rows.Scan(&ps.Platform, &ps.Programs, &ps.Private, &ps.Open, &ps.Bounty, &ps.Removed,
			&in, &outScope, &wc, &dom, &urls, &ips, &cidrs); err != nil {
			return nil, err
		}
		ps.InScopeEntries, ps.OutScopeEntries = int(in.Int64), int(outScope.Int64)
		ps.Targets = map[string]int{
			scope.KindWildcard: int(wc.Int64), scope.KindDomain: int(dom.Int64), scope.KindURL: int(urls.Int64),
			scope.KindIP: int(ips.Int64), scope.KindCIDR: int(cidrs.Int64),
		}
		out = append(out, ps)
	}
	return out, rows.Err()
}

// ChangeView is a change joined with its program, for display.
type ChangeView struct {
	Change
	Program  ProgramRef
	Currency string // the program's, for bounty table changes
	// Asset is the identifier of the scope entry the change is about, as
	// it is now; "" for a change about the program.
	Asset string
	// Flips, with ChangeFilter.CollapseFlips, is how many state changes
	// this one stands for: its program's that week (0 if it stands alone).
	Flips int
}

// CountChanges returns the number of changes recorded by a run.
func (s *Store) CountChanges(ctx context.Context, runID int64) (int, error) {
	var n int
	if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM changes WHERE poll_run_id = ?`, runID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count changes: %w", err)
	}
	return n, nil
}

// UserStatsSnapshot is one stored snapshot of the user's numbers.
type UserStatsSnapshot struct {
	CapturedAt   time.Time
	Reports      int
	ValidReports int
	BountyEarned float64
}

// UserStatsHistory returns a program's stats snapshots, oldest first.
func (s *Store) UserStatsHistory(ctx context.Context, platformName, handle string) ([]UserStatsSnapshot, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT st.captured_at, st.reports, st.valid_reports, st.bounty_earned
		FROM program_user_stats st JOIN programs p ON p.id = st.program_id
		WHERE p.platform = ? AND p.handle = ? ORDER BY st.captured_at`, platformName, handle)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []UserStatsSnapshot
	for rows.Next() {
		var u UserStatsSnapshot
		var captured string
		if err := rows.Scan(&captured, &u.Reports, &u.ValidReports, &u.BountyEarned); err != nil {
			return nil, err
		}
		u.CapturedAt = parseTS(captured)
		out = append(out, u)
	}
	return out, rows.Err()
}

// keepOrder marks an ORDER BY term reverseOrder leaves as it is, as it
// does "x IS NULL": a grouping that holds whichever way the rest goes.
const keepOrder = " /* kept */"

// reverseOrder turns an ORDER BY clause around, term by term: "x DESC"
// becomes "x" and "x" or "x ASC" becomes "x DESC". "x IS NULL" terms, which
// keep missing values last, stay as they are.
func reverseOrder(order string) string {
	var terms []string
	depth, start := 0, 0
	for i, r := range order {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				terms = append(terms, order[start:i])
				start = i + 1
			}
		}
	}
	terms = append(terms, order[start:])
	for i, t := range terms {
		t = strings.TrimSpace(t)
		upper := strings.ToUpper(t)
		switch {
		case strings.HasSuffix(upper, " IS NULL"), strings.HasSuffix(t, keepOrder):
		case strings.HasSuffix(upper, " DESC"):
			t = strings.TrimSpace(t[:len(t)-len(" DESC")])
		case strings.HasSuffix(upper, " ASC"):
			t = strings.TrimSpace(t[:len(t)-len(" ASC")]) + " DESC"
		default:
			t += " DESC"
		}
		terms[i] = t
	}
	return strings.Join(terms, ", ")
}
