package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/scope"
)

// The Targets, Changes and Disclosed pages' filter rails show how many rows
// each choice would list. A choice's count keeps the page's other filters
// and swaps in its own, so a group's counts are what clicking each would
// show.

// TargetFacets counts the Targets page's choices (see TargetFacetsFor).
type TargetFacets struct {
	Kinds     map[string]int // per target kind (not roots)
	Assets    map[string]int // per asset type, of entries (apps, hardware, …)
	Platforms map[string]int
	Added     []int // per cutoff given, in order
	Bounty    int   // bounty-eligible
	Private   int   // on private programs
}

// TargetFacetsFor counts, for f and query (as TargetRows takes them), the
// targets each other kind, platform, "added since" cutoff, "bounty-eligible"
// and "private" would list. Counts are cached until the data changes.
func (s *Store) TargetFacetsFor(ctx context.Context, f TargetFilter, query string, cutoffs []time.Time) (TargetFacets, error) {
	// The counts do not depend on the kind: targetFacets clears it before
	// counting by kind, and assetConds never reads it. Keying on it made every
	// kind filter recompute the same aggregates (measured: 991 ms cold for
	// /targets?kind=domains, 529 ms for /targets).
	f.Order, f.Reverse, f.Kind = "", false, ""
	key := fmt.Sprintf("target-facets:%+v:%q:%v", f, query, cutoffs)
	return cached(ctx, s, key, func() (TargetFacets, error) { return s.targetFacets(ctx, f, query, cutoffs) })
}

func (s *Store) targetFacets(ctx context.Context, f TargetFilter, query string, cutoffs []time.Time) (TargetFacets, error) {
	out := TargetFacets{Kinds: map[string]int{}, Platforms: map[string]int{}, Added: make([]int, len(cutoffs))}
	var err error
	if out.Assets, err = s.assetTypeCounts(ctx, f, query); err != nil {
		return out, err
	}
	// Kinds: every kind but roots, which count apexes rather than targets.
	byKind := f
	byKind.Kind = ""
	conds, args := targetConds(byKind, kindOf(""), query)
	conds = append(conds, "t.kind IN ("+placeholders(len(scope.Kinds))+")")
	for _, k := range scope.Kinds {
		args = append(args, k)
	}
	//nolint:gosec // constant fragments; values are bound parameters
	rows, err := s.db.R.QueryContext(ctx, `SELECT kind, COUNT(*) FROM (SELECT DISTINCT t.kind, e.program_id, t.value
		FROM scope_targets t JOIN scope_entries e ON e.id = t.scope_entry_id JOIN programs p ON p.id = e.program_id
		WHERE `+strings.Join(conds, " AND ")+`) GROUP BY kind`, args...)
	if err != nil {
		return out, fmt.Errorf("target facets by kind: %w", err)
	}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			_ = rows.Close()
			return out, err
		}
		out.Kinds[k] = n
	}
	if err := closeRows(rows); err != nil {
		return out, err
	}

	if len(f.AssetTypes) > 0 {
		return out, s.assetFacets(ctx, f, query, cutoffs, &out)
	}
	// The rest in one pass: each target (per program) with its platform,
	// newest dated entry, bounty and privacy, tallied by those.
	k := kindOf(f.Kind)
	rest := f
	rest.Platform, rest.BountyOnly, rest.PrivateOnly, rest.AddedSince = "", false, false, time.Time{}
	conds, args = targetConds(rest, k, query)
	bucket := "CASE"
	var bargs []any
	for i, c := range cutoffs {
		bucket += fmt.Sprintf(" WHEN a >= ? THEN %d", i)
		bargs = append(bargs, ts(c))
	}
	bucket += fmt.Sprintf(" ELSE %d END", len(cutoffs))
	if len(cutoffs) == 0 {
		bucket = "0"
	}
	since := "1"
	if !f.AddedSince.IsZero() {
		since = "COALESCE(a >= ?, 0)"
		bargs = append(bargs, ts(f.AddedSince))
	}
	//nolint:gosec // constant fragments; values are bound parameters
	rows, err = s.db.R.QueryContext(ctx, `SELECT platform, `+bucket+`, `+since+`, b, pr, COUNT(*) FROM (
		SELECT p.platform, MAX(CASE WHEN e.added_bound = 0 THEN e.added_at END) AS a,
			MAX(e.bounty_eligible = 1 AND p.offers_bounty = 1) AS b, MAX(p.private) AS pr
		FROM scope_targets t`+k.ordered+` JOIN scope_entries e ON e.id = t.scope_entry_id JOIN programs p ON p.id = e.program_id
		WHERE `+strings.Join(conds, " AND ")+` GROUP BY e.program_id, `+k.value+`)
		GROUP BY 1, 2, 3, 4, 5`, append(bargs, args...)...)
	if err != nil {
		return out, fmt.Errorf("target facets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var platform string
		var bucket, n int
		var added, bounty, private bool
		if err := rows.Scan(&platform, &bucket, &added, &bounty, &private, &n); err != nil {
			return out, err
		}
		inPlatform := f.Platform == "" || platform == f.Platform
		bountyOK, privateOK := bounty || !f.BountyOnly, private || !f.PrivateOnly
		if added && bountyOK && privateOK {
			out.Platforms[platform] += n
		}
		if inPlatform && bountyOK && privateOK {
			for i := bucket; i < len(cutoffs); i++ {
				out.Added[i] += n
			}
		}
		if inPlatform && added && privateOK && bounty {
			out.Bounty += n
		}
		if inPlatform && added && bountyOK && private {
			out.Private += n
		}
	}
	return out, rows.Err()
}

// ChangeCount is how many changes of a kind a platform's programs had,
// per period: Bucket is the index of the newest cutoff the changes are
// within (len(cutoffs) for older ones). Bounty says whether the programs
// pay bounties.
type ChangeCount struct {
	Platform string
	Kind     string
	To       string // a state change's new state ("" for other kinds)
	Bounty   bool
	Bucket   int
	N        int
}

// ChangeCounts tallies changes by platform, kind, paying or not and period, for the
// Changes page's filter rail, flip-flops collapsed as Changes does with
// CollapseFlips. keep, if any, counts only those programs' changes (a watchlist).
func (s *Store) ChangeCounts(ctx context.Context, cutoffs []time.Time, keep []ProgramKey) ([]ChangeCount, error) {
	bucket, args := "CASE", []any{ts(s.now()), ""} // every change: no flipsFrom
	for i, c := range cutoffs {
		bucket += fmt.Sprintf(" WHEN c.occurred_at >= ? THEN %d", i)
		args = append(args, ts(c))
	}
	bucket += fmt.Sprintf(" ELSE %d END", len(cutoffs))
	if len(cutoffs) == 0 {
		bucket = "0"
	}
	watch := ""
	if len(keep) > 0 {
		cond, wargs := watchCond(keep)
		watch, args = " AND "+cond, append(args, wargs...)
	}
	//nolint:gosec // constant fragments; values are bound parameters
	rows, err := s.db.R.QueryContext(ctx, `WITH `+flipWeeks+` SELECT p.platform, c.kind, CASE WHEN c.kind = '`+ChangeStateChanged+`' THEN c.new_value ELSE '' END, p.offers_bounty, `+bucket+`, COUNT(*)
		FROM changes c JOIN programs p ON p.id = c.program_id LEFT JOIN flips fw ON fw.id = c.id
		WHERE `+fmt.Sprintf("(fw.id IS NULL OR fw.n < %d OR fw.rn = 1)", FlipFlops)+watch+` GROUP BY 1, 2, 3, 4, 5`, args...)
	if err != nil {
		return nil, fmt.Errorf("change counts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ChangeCount
	for rows.Next() {
		var c ChangeCount
		if err := rows.Scan(&c.Platform, &c.Kind, &c.To, &c.Bounty, &c.Bucket, &c.N); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DisclosureFacets counts the Disclosed page's choices (see
// DisclosureFacetsFor).
type DisclosureFacets struct {
	Platforms  map[string]int
	Severities map[string]int // per stored severity ("" for unrated)
	Since      []int          // per cutoff given, in order
	Paid       int            // with a public bounty
	WithCVE    int            // naming a CVE
	Weaknesses []NamedCount   // the most common, most first
}

// DisclosureFacetsFor counts, for f, the reports each other platform,
// severity, "disclosed since" cutoff, "with a bounty" and weakness would
// list; weaknesses are the top limit of them.
func (s *Store) DisclosureFacetsFor(ctx context.Context, f DisclosureFilter, cutoffs []time.Time, limit int) (DisclosureFacets, error) {
	out := DisclosureFacets{Platforms: map[string]int{}, Severities: map[string]int{}, Since: make([]int, len(cutoffs))}
	rest := f
	rest.Platform, rest.Severity, rest.PaidOnly, rest.WithCVE, rest.Since = "", nil, false, false, time.Time{}
	where, args := rest.where()
	bucket := "CASE"
	var bargs []any
	for i, c := range cutoffs {
		bucket += fmt.Sprintf(" WHEN d.disclosed_at >= ? THEN %d", i)
		bargs = append(bargs, ts(c))
	}
	bucket += fmt.Sprintf(" ELSE %d END", len(cutoffs))
	if len(cutoffs) == 0 {
		bucket = "0"
	}
	since := "1"
	if !f.Since.IsZero() {
		since = "d.disclosed_at >= ?"
		bargs = append(bargs, ts(f.Since))
	}
	//nolint:gosec // constant fragments; values are bound parameters
	rows, err := s.db.R.QueryContext(ctx, `SELECT p.platform, d.severity, `+bucket+`, `+since+`, COALESCE(d.bounty > 0, 0),
		CASE WHEN d.cve_ids IS NOT NULL AND d.cve_ids NOT IN ('', '[]') THEN 1 ELSE 0 END, COUNT(*)
		FROM disclosed_reports d JOIN programs p ON p.id = d.program_id`+where+` GROUP BY 1, 2, 3, 4, 5, 6`, append(bargs, args...)...)
	if err != nil {
		return out, fmt.Errorf("disclosure facets: %w", err)
	}
	for rows.Next() {
		var platform, severity string
		var bucket, n int
		var inSince, paid, hasCVE bool
		if err := rows.Scan(&platform, &severity, &bucket, &inSince, &paid, &hasCVE, &n); err != nil {
			_ = rows.Close()
			return out, err
		}
		inPlatform := f.Platform == "" || platform == f.Platform
		inSeverity := len(f.Severity) == 0 || slices.Contains(f.Severity, severity)
		paidOK := paid || !f.PaidOnly
		if inSeverity && inSince && paidOK {
			out.Platforms[platform] += n
		}
		if inPlatform && inSince && paidOK {
			out.Severities[severity] += n
		}
		if inPlatform && inSeverity && paidOK {
			for i := bucket; i < len(cutoffs); i++ {
				out.Since[i] += n
			}
		}
		if inPlatform && inSeverity && inSince && paid {
			out.Paid += n
		}
		if inPlatform && inSeverity && inSince && paidOK && hasCVE {
			out.WithCVE += n
		}
	}
	if err := closeRows(rows); err != nil {
		return out, err
	}

	byWeakness := f
	byWeakness.Weakness = ""
	where, args = byWeakness.where()
	if where == "" {
		where = " WHERE d.weakness <> ''"
	} else {
		where += " AND d.weakness <> ''"
	}
	//nolint:gosec // constant fragments; values are bound parameters
	rows, err = s.db.R.QueryContext(ctx, `SELECT d.weakness, COUNT(*) FROM disclosed_reports d JOIN programs p ON p.id = d.program_id`+
		where+` GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT ?`, append(args, limit)...)
	if err != nil {
		return out, fmt.Errorf("disclosure facets by weakness: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var c NamedCount
		if err := rows.Scan(&c.Name, &c.Count); err != nil {
			return out, err
		}
		out.Weaknesses = append(out.Weaknesses, c)
	}
	return out, rows.Err()
}

// closeRows closes rows and returns the error, if any, that ended them.
func closeRows(rows interface {
	Err() error
	Close() error
}) error {
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	return rows.Close()
}
