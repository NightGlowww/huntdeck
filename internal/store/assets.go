package store

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// Assets that aren't addresses (apps, executables, hardware, source code,
// …) have no targets in scope_targets. With TargetFilter.AssetTypes set,
// TargetRows, EachTarget and TargetFacetsFor read their scope entries
// instead: each live entry is a row, its identifier the value and its asset
// type the kind. There are few, so a list is read whole and sorted here.

// assetConds are the conditions for f's assets and query; withType false
// leaves the asset types out (for counting each type).
func assetConds(f TargetFilter, query string, withType bool) ([]string, []any) {
	conds := []string{"e.in_scope = ?", "e.removed_at IS NULL", "p.removed_at IS NULL"}
	args := []any{b2i(!f.OutOfScope)}
	if withType {
		conds = append(conds, "e.asset_type IN ("+placeholders(len(f.AssetTypes))+")")
		for _, t := range f.AssetTypes {
			args = append(args, t)
		}
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
		conds = append(conds, `lower(e.identifier) LIKE ? ESCAPE '\'`)
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
	if c, cargs := excludeCond(f.Exclude, "e.identifier"); c != "" {
		conds, args = append(conds, c), append(args, cargs...)
	}
	return conds, args
}

// assetList is every asset matching f and query, in f's order: by
// identifier, newest first (TargetsByNewest) or highest bounty first
// (TargetsByBounty, Reverse turning it around).
func (s *Store) assetList(ctx context.Context, f TargetFilter, query string) ([]TargetRow, error) {
	conds, args := assetConds(f, query, true)
	//nolint:gosec // constant fragments; values are bound parameters
	rows, err := s.db.R.QueryContext(ctx, `SELECT e.asset_type, e.identifier, e.in_scope, e.bounty_eligible, e.added_at,
			p.id, p.platform, p.handle, p.name, p.url, p.private, p.state, p.offers_bounty,
			(SELECT ts.score FROM target_scores ts WHERE ts.program_id = e.program_id AND ts.kind = e.asset_type AND ts.value = e.identifier),
			p.rewards, p.bounty_min, p.bounty_max, ps.top_bounty_high, COALESCE(ps.currency, p.currency, '')
		FROM scope_entries e JOIN programs p ON p.id = e.program_id
		LEFT JOIN program_public_stats ps ON ps.program_id = p.id AND ps.captured_at = p.stats_at
		WHERE `+strings.Join(conds, " AND "), args...)
	if err != nil {
		return nil, fmt.Errorf("assets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []TargetRow
	tables := map[int64][]platform.RewardRow{}
	for rows.Next() {
		var r TargetRow
		var added, rewards sql.NullString
		var score, listMin, listMax, statsTop sql.NullFloat64
		p := &r.Program
		if err := rows.Scan(&r.Kind, &r.Value, &r.InScope, &r.Bounty, &added, &p.ID, &p.Platform, &p.Handle, &p.Name, &p.URL, &p.Private, &p.State,
			&r.OffersBounty, &score, &rewards, &listMin, &listMax, &statsTop, &r.Currency); err != nil {
			return nil, err
		}
		r.AddedAt = parseTSPtr(added)
		r.Score, r.Scored = score.Float64, score.Valid
		r.Bounty = r.Bounty && r.OffersBounty
		if r.Bounty {
			table, ok := tables[p.ID]
			if !ok && rewards.Valid {
				_ = json.Unmarshal([]byte(rewards.String), &table) // a bad table reads as none
				tables[p.ID] = table
			}
			r.PayMin, r.PayMax, r.PayFrom = targetBounty(table, r.Value, floatPtr(listMin), floatPtr(listMax), floatPtr(statsTop))
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	byValue := func(a, b TargetRow) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Value), strings.ToLower(b.Value)), cmp.Compare(a.Program.ID, b.Program.ID))
	}
	newest := func(a, b TargetRow) int {
		switch {
		case a.AddedAt == nil && b.AddedAt == nil:
			return 0
		case a.AddedAt == nil: // undated last
			return 1
		case b.AddedAt == nil:
			return -1
		}
		return b.AddedAt.Compare(*a.AddedAt)
	}
	switch f.Order {
	case TargetsByNewest:
		slices.SortStableFunc(out, func(a, b TargetRow) int { return cmp.Or(newest(a, b), byValue(a, b)) })
	case TargetsByBounty:
		slices.SortStableFunc(out, func(a, b TargetRow) int {
			return cmp.Or(cmpBounty(a, b, f.Reverse), newest(a, b), byValue(a, b))
		})
	default:
		slices.SortStableFunc(out, byValue)
	}
	if f.OnePerProgram {
		// After the sort, so the row kept is the one the chosen order puts first:
		// the newest, or the best paying. The page's total is counted from this
		// list, so the number and the rows agree.
		seen := make(map[int64]bool, len(out))
		kept := out[:0]
		for _, r := range out {
			if seen[r.Program.ID] {
				continue
			}
			seen[r.Program.ID] = true
			kept = append(kept, r)
		}
		out = kept
	}
	return out, nil
}

// assetRows is one page of assetList, and its length.
func (s *Store) assetRows(ctx context.Context, f TargetFilter, query string, limit, offset int) ([]TargetRow, int, error) {
	all, err := s.assetList(ctx, f, query)
	if err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 100
	}
	page := all[min(len(all), max(0, offset)):]
	return page[:min(len(page), limit)], len(all), nil
}

// eachAsset calls fn with each distinct asset matching f, by identifier,
// paged by f.Limit and f.Offset.
func (s *Store) eachAsset(ctx context.Context, f TargetFilter, fn func(string) error) error {
	f.Order = ""
	all, err := s.assetList(ctx, f, f.Query)
	if err != nil {
		return err
	}
	var values []string
	for _, r := range all {
		if len(values) == 0 || values[len(values)-1] != r.Value {
			values = append(values, r.Value)
		}
	}
	values = values[min(len(values), max(0, f.Offset)):]
	if f.Limit > 0 {
		values = values[:min(len(values), f.Limit)]
	}
	for _, v := range values {
		if err := fn(v); err != nil {
			return err
		}
	}
	return nil
}

// assetTypeCounts counts the live entries of each asset type for f and
// query, f's own asset types aside.
func (s *Store) assetTypeCounts(ctx context.Context, f TargetFilter, query string) (map[string]int, error) {
	conds, args := assetConds(f, query, false)
	//nolint:gosec // constant fragments; values are bound parameters
	rows, err := s.db.R.QueryContext(ctx, `SELECT e.asset_type, COUNT(*) FROM scope_entries e JOIN programs p ON p.id = e.program_id
		WHERE `+strings.Join(conds, " AND ")+` GROUP BY 1`, args...)
	if err != nil {
		return nil, fmt.Errorf("asset counts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, rows.Err()
}

// assetFacets fills out's platform, "added since", bounty and private
// counts for f's assets, as targetFacets does for targets.
func (s *Store) assetFacets(ctx context.Context, f TargetFilter, query string, cutoffs []time.Time, out *TargetFacets) error {
	rest := f
	rest.Platform, rest.BountyOnly, rest.PrivateOnly, rest.AddedSince = "", false, false, time.Time{}
	all, err := s.assetList(ctx, rest, query)
	if err != nil {
		return err
	}
	for _, r := range all {
		inPlatform := f.Platform == "" || r.Program.Platform == f.Platform
		added := f.AddedSince.IsZero() || (r.AddedAt != nil && !r.AddedAt.Before(f.AddedSince))
		bountyOK, privateOK := r.Bounty || !f.BountyOnly, r.Program.Private || !f.PrivateOnly
		if added && bountyOK && privateOK {
			out.Platforms[r.Program.Platform]++
		}
		if inPlatform && bountyOK && privateOK && r.AddedAt != nil {
			for i, c := range cutoffs {
				if !r.AddedAt.Before(c) {
					out.Added[i]++
				}
			}
		}
		if inPlatform && added && privateOK && r.Bounty {
			out.Bounty++
		}
		if inPlatform && added && bountyOK && r.Program.Private {
			out.Private++
		}
	}
	return nil
}
