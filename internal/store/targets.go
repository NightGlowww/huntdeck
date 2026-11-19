package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

// TargetRow is one target and the program it belongs to.
type TargetRow struct {
	Kind    string
	Value   string
	InScope bool
	Bounty  bool
	AddedAt *time.Time // when its scope entry was added, if known
	// Joined says it's new to the user rather than to its program, from
	// BestTargets: in scope when they got access to its private program,
	// AddedAt being when they did. MoreJoined, on the last of a program's
	// targets TargetFilter.JoinedPerProgram lets through, counts the rest.
	Joined     bool
	MoreJoined int
	Program    ProgramRef
	Score      float64  // its asset score: BestTargets', or TargetRows' where it has one
	Scored     bool     // Score is known
	Reasons    []string // why it scores what it does, from BestTargets
	// OffersBounty is whether its program pays bounties, from TargetRows.
	OffersBounty bool
	// From BestTargets, for a bounty-eligible target: the bounty range its
	// program gives for it (PayMin nil when only a top is known).
	PayMin, PayMax *float64
	PayFrom        string // PayFromTable, PayFromListing or PayFromStats
	Currency       string
	// PaidAll is the program's bounties paid all time, in Currency, from
	// BestTargets: as ProgramListItem.PaidAll, with Rewarded and AvgPayout
	// behind an estimate.
	PaidAll          *float64
	PaidAllEstimated bool
	Rewarded         *int
	AvgPayout        *float64
}

// TargetRows returns one page of targets matching f (plus an optional
// substring query), one row per target and program, ordered by value (or
// newest first for TargetsByNewest, highest bounty first for
// TargetsByBounty), with each bounty-eligible one's bounty range, and
// the total number of such rows.
func (s *Store) TargetRows(ctx context.Context, f TargetFilter, query string, limit, offset int) ([]TargetRow, int, error) {
	if len(f.AssetTypes) > 0 {
		return s.assetRows(ctx, f, query, limit, offset)
	}
	k := kindOf(f.Kind)
	conds, args := targetConds(f, k, query)
	// A program can list the same target twice (say, as a URL and a domain),
	// and many targets share a root domain; keep only the program's first
	// matching row so totals and offsets stay exact. That's one row per
	// program and value, which is what the count counts, without the per-row
	// check.
	joins := ` JOIN scope_entries e ON e.id = t.scope_entry_id JOIN programs p ON p.id = e.program_id
		WHERE ` + strings.Join(conds, " AND ")
	countArgs := slices.Clone(args)
	dup := ""
	if f.BountyOnly {
		dup = " AND e2.bounty_eligible = 1"
	}
	if !f.AddedSince.IsZero() {
		dup += " AND e2.added_at >= ? AND e2.added_bound = 0"
		args = append(args, ts(f.AddedSince))
	}
	if !f.ChangedSince.IsZero() {
		dup += " AND e2.updated_at > ?"
		args = append(args, ts(f.ChangedSince))
	}
	conds = append(conds, `NOT EXISTS (SELECT 1 FROM scope_targets t2 JOIN scope_entries e2 ON e2.id = t2.scope_entry_id
		WHERE `+k.same("t2")+` AND t2.id < t.id AND e2.program_id = e.program_id
			AND e2.in_scope = e.in_scope AND e2.removed_at IS NULL`+dup+`)`)
	// Newest and bounty orders sort by when an entry was added. Walking the
	// entries (scope_entries_order) and reaching their targets stops after the
	// page's rows; walking the targets instead sorts every match first (about
	// 250 ms for 39k domains, and it grows). CROSS JOIN fixes the outer side.
	from := ` FROM scope_targets t` + k.ordered + ` JOIN scope_entries e ON e.id = t.scope_entry_id JOIN programs p ON p.id = e.program_id
		WHERE ` + strings.Join(conds, " AND ")
	if f.Order == TargetsByNewest || f.Order == TargetsByBounty {
		from = ` FROM scope_entries e CROSS JOIN scope_targets t ON t.scope_entry_id = e.id JOIN programs p ON p.id = e.program_id
			WHERE ` + strings.Join(conds, " AND ")
	}

	// Counting needs every matching row, so totals are cached until the data
	// changes; pages walk the value-ordered index and stop early.
	total, err := s.cachedCount(ctx, fmt.Sprintf("targets:%+v:%q", f, query), func() (int, error) {
		var n int
		err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT DISTINCT e.program_id, `+k.value+
			` FROM scope_targets t`+k.ordered+joins+`)`, countArgs...).Scan(&n)
		return n, err
	})
	if err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 100
	}
	// Bounty-eligible if any of the program's entries for this target is.
	kindCol := "t.kind"
	if f.Kind == scope.KindRoot {
		kindCol = "'" + scope.KindRoot + "'"
	}
	sel := `SELECT ` + kindCol + `, ` + k.value + `, e.in_scope, e.bounty_eligible OR EXISTS (SELECT 1 FROM scope_targets t3
			JOIN scope_entries e3 ON e3.id = t3.scope_entry_id
			WHERE ` + k.same("t3") + ` AND e3.program_id = e.program_id
				AND e3.in_scope = e.in_scope AND e3.removed_at IS NULL AND e3.bounty_eligible = 1),
		e.added_at, p.id, p.platform, p.handle, p.name, p.url, p.private, p.state, p.offers_bounty,
		(SELECT ts.score FROM target_scores ts WHERE ts.program_id = e.program_id AND ts.kind = t.kind AND ts.value = ` + k.value + `),
		p.rewards, p.bounty_min, p.bounty_max, ps.top_bounty_high, COALESCE(ps.currency, p.currency, ''), t.id`
	order := k.value + `, t.id`
	if f.Order == TargetsByNewest || f.Order == TargetsByBounty {
		// Undated entries (already in scope when tracking began, on
		// platforms that don't date them) go last.
		order = `e.added_at IS NULL, e.added_at DESC, ` + order
	}
	withStats := strings.Replace(from, ` JOIN programs p ON p.id = e.program_id`, ` JOIN programs p ON p.id = e.program_id
		LEFT JOIN program_public_stats ps ON ps.program_id = p.id AND ps.captured_at = p.stats_at`, 1)
	q := sel + withStats + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?` //nolint:gosec // constant fragments; values are bound parameters
	qargs := append(slices.Clone(args), limit, max(0, offset))
	var page []int64 // by bounty: this page's targets, in order
	if f.Order == TargetsByBounty {
		// A target's bounty is worked out in Go, so the order is too, over
		// every match, and kept until the data changes; then just this
		// page's rows are read.
		ids, err := cached(ctx, s, fmt.Sprintf("targets-bounty:%+v:%q", f, query), func() ([]int64, error) {
			return s.targetsByBounty(ctx, f.Reverse, k, from, order, args)
		})
		if err != nil {
			return nil, 0, err
		}
		page = ids[min(len(ids), max(0, offset)):]
		page = page[:min(len(page), limit)]
		if len(page) == 0 {
			return nil, total, nil
		}
		q = sel + withStats + ` AND t.id IN (` + placeholders(len(page)) + `)` //nolint:gosec // constant fragments; values are bound parameters
		qargs = slices.Clone(args)
		for _, id := range page {
			qargs = append(qargs, id)
		}
	}
	//nolint:gosec // q is built from constant fragments; every value is bound
	rows, err := s.db.R.QueryContext(ctx, q, qargs...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var out []TargetRow
	var ids []int64
	tables := map[int64][]platform.RewardRow{} // each program's bounty table, read once
	for rows.Next() {
		var r TargetRow
		var id int64
		var added, rewards sql.NullString
		var score, listMin, listMax, statsTop sql.NullFloat64
		p := &r.Program
		if err := rows.Scan(&r.Kind, &r.Value, &r.InScope, &r.Bounty, &added, &p.ID, &p.Platform, &p.Handle, &p.Name, &p.URL, &p.Private, &p.State,
			&r.OffersBounty, &score, &rewards, &listMin, &listMax, &statsTop, &r.Currency, &id); err != nil {
			return nil, 0, err
		}
		r.AddedAt = parseTSPtr(added)
		r.Score, r.Scored = score.Float64, score.Valid
		if r.Bounty && r.OffersBounty { // what a target that pays nothing "pays" is beside the point
			table, ok := tables[p.ID]
			if !ok && rewards.Valid {
				_ = json.Unmarshal([]byte(rewards.String), &table) // a bad table reads as none
				tables[p.ID] = table
			}
			r.PayMin, r.PayMax, r.PayFrom = targetBounty(table, r.Value, floatPtr(listMin), floatPtr(listMax), floatPtr(statsTop))
		}
		out = append(out, r)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if page != nil { // back in the page's order
		at := map[int64]TargetRow{}
		for i, id := range ids {
			at[id] = out[i]
		}
		out = out[:0]
		for _, id := range page {
			if r, ok := at[id]; ok {
				out = append(out, r)
			}
		}
	}
	return out, total, nil
}

// targetsByBounty is every target from (TargetRows' FROM and WHERE, args
// its arguments) sorted by bounty, then by order: their scope_targets IDs.
// It reads just what a bounty needs, with each program's bounty table once.
func (s *Store) targetsByBounty(ctx context.Context, reverse bool, k targetKind, from, order string, args []any) ([]int64, error) {
	type pay struct {
		table            []platform.RewardRow
		listMin, listMax *float64
		statsTop         *float64
	}
	pays := map[int64]pay{}
	prows, err := s.db.R.QueryContext(ctx, `SELECT p.id, p.rewards, p.bounty_min, p.bounty_max, ps.top_bounty_high FROM programs p
		LEFT JOIN program_public_stats ps ON ps.program_id = p.id AND ps.captured_at = p.stats_at
		WHERE p.removed_at IS NULL AND p.offers_bounty = 1`)
	if err != nil {
		return nil, fmt.Errorf("program bounties: %w", err)
	}
	for prows.Next() {
		var id int64
		var rewards sql.NullString
		var lo, hi, top sql.NullFloat64
		if err := prows.Scan(&id, &rewards, &lo, &hi, &top); err != nil {
			_ = prows.Close()
			return nil, err
		}
		var table []platform.RewardRow
		if rewards.Valid {
			_ = json.Unmarshal([]byte(rewards.String), &table) // a bad table reads as none
		}
		pays[id] = pay{table, floatPtr(lo), floatPtr(hi), floatPtr(top)}
	}
	if err := closeRows(prows); err != nil {
		return nil, err
	}
	//nolint:gosec // constant fragments; values are bound parameters
	rows, err := s.db.R.QueryContext(ctx, `SELECT t.id, `+k.value+`, e.program_id, e.bounty_eligible OR EXISTS (SELECT 1 FROM scope_targets t3
			JOIN scope_entries e3 ON e3.id = t3.scope_entry_id
			WHERE `+k.same("t3")+` AND e3.program_id = e.program_id
				AND e3.in_scope = e.in_scope AND e3.removed_at IS NULL AND e3.bounty_eligible = 1)`+from+` ORDER BY `+order, args...)
	if err != nil {
		return nil, fmt.Errorf("targets by bounty: %w", err)
	}
	defer func() { _ = rows.Close() }()
	type keyed struct {
		id  int64
		row TargetRow
	}
	var all []keyed
	for rows.Next() {
		var x keyed
		var program int64
		var bounty bool
		if err := rows.Scan(&x.id, &x.row.Value, &program, &bounty); err != nil {
			return nil, err
		}
		if p, ok := pays[program]; ok && bounty {
			x.row.PayMin, x.row.PayMax, _ = targetBounty(p.table, x.row.Value, p.listMin, p.listMax, p.statsTop)
		}
		all = append(all, x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.SortStableFunc(all, func(a, b keyed) int { return cmpBounty(a.row, b.row, reverse) })
	ids := make([]int64, len(all))
	for i, x := range all {
		ids[i] = x.id
	}
	return ids, nil
}

// targetConds are the conditions TargetRows applies for f and query to
// scope_targets t, scope_entries e and programs p, with their arguments.
func targetConds(f TargetFilter, k targetKind, query string) ([]string, []any) {
	conds := []string{"e.in_scope = ?", "e.removed_at IS NULL", "p.removed_at IS NULL"}
	args := []any{b2i(!f.OutOfScope)}
	if f.Kind != "" {
		conds = append(conds, k.cond)
		args = append(args, k.args...)
	}
	if f.Platform != "" {
		conds = append(conds, "p.platform = ?")
		args = append(args, f.Platform)
	}
	if f.BountyOnly {
		conds = append(conds, "e.bounty_eligible = 1", "p.offers_bounty = 1")
	}
	if f.PrivateOnly {
		conds = append(conds, "p.private = 1")
	}
	if !f.IncludePaused {
		conds = append(conds, "p.state = 'open'")
	}
	if !f.IncludeIgnored {
		conds = append(conds, "p.ignored = 0")
	}
	if f.Tag != "" {
		conds = append(conds, tagCond)
		args = append(args, f.Tag)
	}
	if q := strings.TrimSpace(query); q != "" {
		conds = append(conds, `lower(`+k.value+`) LIKE ? ESCAPE '\'`)
		args = append(args, likeEscape(q))
	}
	if !f.AddedSince.IsZero() {
		conds = append(conds, "e.added_at >= ?", "e.added_bound = 0")
		args = append(args, ts(f.AddedSince))
	}
	if !f.ChangedSince.IsZero() {
		conds = append(conds, "e.updated_at > ?")
		args = append(args, ts(f.ChangedSince))
	}
	if c, cargs := excludeCond(f.Exclude, k.value); c != "" {
		conds, args = append(conds, c), append(args, cargs...)
	}
	return conds, args
}
