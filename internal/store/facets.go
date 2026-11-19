package store

import (
	"context"
	"strings"
	"time"
)

// ProgramFacets counts, for each choice in the programs page's filter rail,
// the programs it would show: the current filter with that choice's group
// set to it (so choices within a group don't narrow each other).
type ProgramFacets struct {
	Platforms map[string]int
	Bounty    int // pays bounties
	VDP       int // doesn't
	// TopBounty counts by minimum top bounty (FacetTopBounties).
	TopBounty map[float64]int
	// NewScope counts by window (FacetNewScope); Launched and Resumed by
	// FacetLaunched and FacetResumed.
	NewScope          map[time.Duration]int
	Launched, Resumed int
	Wildcards, Apps   int
	APIs, Credentials int
	NoRequirements    int
	// InvitesPending and InvitesJoined count the Invite filter's choices.
	InvitesPending, InvitesJoined int
	// IgnoredOnly counts the programs the user ignored (the Ignored
	// filter's "only" choice); WithIgnored counts every program with them
	// in (its "include" choice).
	IgnoredOnly int
	WithIgnored int
	Total       int
}

// The choices ProgramFacets counts.
var (
	FacetTopBounties = []float64{20000, 5000, 1000}
	FacetNewScope    = []time.Duration{7 * 24 * time.Hour, 30 * 24 * time.Hour}
)

const (
	FacetLaunched = 90 * 24 * time.Hour
	FacetResumed  = 30 * 24 * time.Hour
)

// facet is one counted choice: its condition, and where its count goes.
type facet struct {
	cond string
	args []any
	into *int
}

// facetGroup is a set of choices that replace each other: their counts
// ignore the filter's own setting for the group.
type facetGroup struct {
	active bool                   // the filter sets this group
	clear  func(f *ProgramFilter) // drops the group from the filter
	facets []facet
}

// ProgramFacets counts the filter rail's choices for f (Limit, Offset and
// Sort are ignored) among the given platforms. Groups the filter doesn't set are counted together in one
// pass; each set group needs its own.
func (s *Store) ProgramFacets(ctx context.Context, f ProgramFilter, platforms []string) (ProgramFacets, error) {
	now := s.Now()
	fc := ProgramFacets{Platforms: map[string]int{}, TopBounty: map[float64]int{}, NewScope: map[time.Duration]int{}}
	// Map values can't be counted into, so these groups count into slices.
	platformN := make([]int, len(platforms))
	platformGroup := facetGroup{active: len(f.Platforms) > 0, clear: func(f *ProgramFilter) { f.Platforms = nil }}
	for i, p := range platforms {
		platformGroup.facets = append(platformGroup.facets, facet{"p.platform = ?", []any{p}, &platformN[i]})
	}
	topN := make([]int, len(FacetTopBounties))
	topGroup := facetGroup{active: f.MinBounty > 0, clear: func(f *ProgramFilter) { f.MinBounty = 0 }}
	for i, v := range FacetTopBounties {
		topGroup.facets = append(topGroup.facets, facet{TopBountyExpr + " >= ?", []any{v}, &topN[i]})
	}
	newN := make([]int, len(FacetNewScope))
	newGroup := facetGroup{active: !f.NewScopeSince.IsZero(), clear: func(f *ProgramFilter) { f.NewScopeSince = time.Time{} }}
	for i, d := range FacetNewScope {
		newGroup.facets = append(newGroup.facets, facet{newScopeCond, []any{ts(now.Add(-d))}, &newN[i]})
	}
	groups := []facetGroup{
		platformGroup, topGroup, newGroup,
		{f.BountyOnly || f.NoBounty, func(f *ProgramFilter) { f.BountyOnly, f.NoBounty = false, false },
			[]facet{{"p.offers_bounty = 1", nil, &fc.Bounty}, {"p.offers_bounty = 0", nil, &fc.VDP}}},
		{!f.LaunchedSince.IsZero(), func(f *ProgramFilter) { f.LaunchedSince = time.Time{} },
			[]facet{{"p.started_at >= ?", []any{ts(now.Add(-FacetLaunched))}, &fc.Launched}}},
		{!f.ResumedSince.IsZero(), func(f *ProgramFilter) { f.ResumedSince = time.Time{} },
			[]facet{{"p.state = 'open' AND " + resumedAt + " >= ?", []any{ts(now.Add(-FacetResumed))}, &fc.Resumed}}},
		{f.Wildcards, func(f *ProgramFilter) { f.Wildcards = false }, []facet{{"p.n_wildcards > 0", nil, &fc.Wildcards}}},
		{f.Apps, func(f *ProgramFilter) { f.Apps = false }, []facet{{appCount + " > 0", nil, &fc.Apps}}},
		{f.APIs, func(f *ProgramFilter) { f.APIs = false }, []facet{{apiCount + " > 0", nil, &fc.APIs}}},
		{f.CredentialsOnly, func(f *ProgramFilter) { f.CredentialsOnly = false }, []facet{{testAccountsCond, nil, &fc.Credentials}}},
		{f.NoRequirements, func(f *ProgramFilter) { f.NoRequirements = false }, []facet{{noRequirementsCond, nil, &fc.NoRequirements}}},
		{f.Invite != "", func(f *ProgramFilter) { f.Invite = "" },
			[]facet{{invitePendingCond, nil, &fc.InvitesPending}, {inviteJoinedCond, nil, &fc.InvitesJoined}}},
		// Always its own pass, with ignored programs in it: the default
		// filter leaves them out, so counting them with the others would
		// always be zero.
		{true, func(f *ProgramFilter) { f.Ignored = "include" },
			[]facet{{"p.ignored = 1", nil, &fc.IgnoredOnly}, {"1", nil, &fc.WithIgnored}}},
		{false, nil, []facet{{"1", nil, &fc.Total}}},
	}

	var together []facet
	for _, g := range groups {
		if !g.active {
			together = append(together, g.facets...)
			continue
		}
		gf := f
		g.clear(&gf)
		if err := s.countFacets(ctx, gf, g.facets); err != nil {
			return fc, err
		}
	}
	if err := s.countFacets(ctx, f, together); err != nil {
		return fc, err
	}
	for i, p := range platforms {
		fc.Platforms[p] = platformN[i]
	}
	for i, v := range FacetTopBounties {
		fc.TopBounty[v] = topN[i]
	}
	for i, d := range FacetNewScope {
		fc.NewScope[d] = newN[i]
	}
	return fc, nil
}

// countFacets counts, in one pass over the programs f selects, those
// meeting each facet's condition.
func (s *Store) countFacets(ctx context.Context, f ProgramFilter, facets []facet) error {
	if len(facets) == 0 {
		return nil
	}
	sums := make([]string, len(facets))
	var args []any
	dest := make([]any, len(facets))
	for i, fa := range facets {
		sums[i] = "COALESCE(SUM(CASE WHEN " + fa.cond + " THEN 1 ELSE 0 END), 0)"
		args = append(args, fa.args...)
		dest[i] = fa.into
	}
	where, wargs := f.where()
	q := `SELECT ` + strings.Join(sums, ", ") + ` FROM programs p` + where //nolint:gosec // constant fragments; values are bound parameters
	return s.db.R.QueryRowContext(ctx, q, append(args, wargs...)...).Scan(dest...)
}
