package issuehunt

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

// FetchScope implements platform.Platform.
func (c *Client) FetchScope(ctx context.Context, p platform.Program) ([]platform.ScopeEntry, error) {
	entries, _, err := c.FetchScopeDetail(ctx, p)
	return entries, err
}

// FetchScopeDetail implements platform.ScopeDetailFetcher.
// It uses the program struct cached by ListPrograms when available and falls
// back to GET /programs/{slugOrId} otherwise.
func (c *Client) FetchScopeDetail(ctx context.Context, p platform.Program) ([]platform.ScopeEntry, platform.ScopeDetail, error) {
	pj, ok := c.cachedProgram(p.Handle, p.PlatformID)
	if !ok {
		slugOrID := firstNonEmpty(p.Handle, p.PlatformID)
		if slugOrID == "" {
			return nil, platform.ScopeDetail{}, fmt.Errorf("issuehunt: empty program handle")
		}
		var err error
		pj, err = c.fetchProgramDetail(ctx, slugOrID)
		if err != nil {
			return nil, platform.ScopeDetail{}, err
		}
	}

	if pj.Visibility == "application" && len(pj.ScopeGroups) == 0 && len(pj.Contents) == 0 {
		return nil, buildScopeDetail(pj), fmt.Errorf("issuehunt: program %s scope unavailable until application is approved: %w",
			firstNonEmpty(pj.Slug, p.Handle, pj.ID), platform.ErrScopeUnavailable)
	}

	entries := parseScopeGroups(pj.ScopeGroups, programOffersBounty(pj))
	return entries, buildScopeDetail(pj), nil
}

func parseScopeGroups(groups []scopeGroupJSON, bountyEligible bool) []platform.ScopeEntry {
	var out []platform.ScopeEntry
	seen := map[string]bool{}
	add := func(e platform.ScopeEntry) {
		k := fmt.Sprintf("%s|%s", e.AssetType, strings.ToLower(e.Identifier))
		if e.Identifier == "" || seen[k] {
			return
		}
		seen[k] = true
		out = append(out, e)
	}

	for _, sg := range groups {
		ident := strings.TrimSpace(sg.Identifier)
		if ident == "" {
			continue
		}
		platformType := strings.Join(sg.Type, ",")
		instr := strings.TrimSpace(sg.Description)
		maxSev := maxGroupSeverity(sg.Scopes)

		primary := primaryGroupAssetType(sg.Type)
		if primary == scope.AssetIOS || primary == scope.AssetAndroid || primary == scope.AssetExecutable {
			parts := splitCommaTargets(ident)
			for _, part := range parts {
				pid := ""
				if len(parts) == 1 {
					pid = sg.ID
				}
				add(platform.ScopeEntry{
					PlatformID:        pid,
					InScope:           true,
					AssetType:         primary,
					PlatformAssetType: platformType,
					Identifier:        part,
					BountyEligible:    bountyEligible,
					MaxSeverity:       maxSev,
					Instruction:       instr,
				})
			}
			continue
		}

		// Web/domain/spa/api/other: split comma-separated targets cleanly with internal/scope.
		targets := scope.Targets(primary, ident)
		if len(targets) > 0 {
			for _, t := range targets {
				pid := ""
				if len(targets) == 1 {
					pid = sg.ID
				}
				add(platform.ScopeEntry{
					PlatformID:        pid,
					InScope:           true,
					AssetType:         t.Kind,
					PlatformAssetType: platformType,
					Identifier:        t.Value,
					BountyEligible:    bountyEligible,
					MaxSeverity:       maxSev,
					Instruction:       instr,
				})
			}
			continue
		}

		// Fallback if internal/scope didn't extract web hosts (e.g. "other" descriptive target).
		parts := splitCommaTargets(ident)
		for _, part := range parts {
			pid := ""
			if len(parts) == 1 {
				pid = sg.ID
			}
			add(platform.ScopeEntry{
				PlatformID:        pid,
				InScope:           true,
				AssetType:         inferFallbackAssetType(primary, part),
				PlatformAssetType: platformType,
				Identifier:        part,
				BountyEligible:    bountyEligible,
				MaxSeverity:       maxSev,
				Instruction:       instr,
			})
		}
	}
	return out
}

func splitCommaTargets(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func primaryGroupAssetType(types []string) string {
	for _, raw := range types {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "ios":
			return scope.AssetIOS
		case "android":
			return scope.AssetAndroid
		case "executable":
			return scope.AssetExecutable
		}
	}
	for _, raw := range types {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "api":
			return scope.AssetAPI
		case "domain", "spa":
			return scope.AssetDomain
		}
	}
	return scope.AssetOther
}

func inferFallbackAssetType(primary, ident string) string {
	if _, err := netip.ParsePrefix(ident); err == nil {
		return scope.AssetCIDR
	}
	if _, err := netip.ParseAddr(ident); err == nil {
		return scope.AssetIP
	}
	if primary == scope.AssetAPI {
		return scope.AssetAPI
	}
	if primary == scope.AssetDomain {
		return scope.WebType(ident)
	}
	return scope.AssetOther
}

func maxGroupSeverity(rules []scopeRuleJSON) string {
	rank := func(s string) int {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "critical":
			return 5
		case "high":
			return 4
		case "medium":
			return 3
		case "low":
			return 2
		case "informational", "none":
			return 1
		default:
			return 0
		}
	}
	best := 0
	for _, r := range rules {
		if n := rank(r.Criticality); n > best {
			best = n
		}
	}
	switch best {
	case 5:
		return platform.SeverityCritical
	case 4:
		return platform.SeverityHigh
	case 3:
		return platform.SeverityMedium
	case 2:
		return platform.SeverityLow
	case 1:
		return platform.SeverityNone
	default:
		return ""
	}
}

func buildScopeDetail(pj programJSON) platform.ScopeDetail {
	return platform.ScopeDetail{
		Policy:  extractPolicy(pj.Contents),
		Rewards: buildRewards(pj),
		Facts:   buildFacts(pj),
		Requirements: &platform.Requirements{
			Application: pj.Visibility == "application",
		},
		State: mapProgramState(pj),
	}
}

func buildFacts(pj programJSON) *platform.Facts {
	started := parseTime(pj.FirstActivated)
	offers := programOffersBounty(pj)
	return &platform.Facts{
		StartedAt:    started,
		OffersBounty: &offers,
	}
}

func buildRewards(pj programJSON) []platform.RewardRow {
	if isPointCreditSystem(pj) || strings.EqualFold(strings.TrimSpace(pj.Type), "vdp") || pj.Rewards == nil {
		return nil
	}
	var rows []platform.RewardRow
	if cr := pj.Rewards.CriticalityRewards; cr != nil {
		row := platform.RewardRow{
			Scope:    "Default",
			Low:      tierToRange(cr.Low),
			Medium:   tierToRange(cr.Medium),
			High:     tierToRange(cr.High),
			Critical: tierToRange(cr.Critical),
		}
		if rowHasReward(row) {
			rows = append(rows, row)
		}
	}

	vrtIdx := map[string]int{}
	for _, v := range pj.Rewards.VRTs {
		rr := makeRewardRange(v.From, v.To)
		if rr.Max <= 0 {
			continue
		}
		scopeName := strings.TrimSpace(v.VRTID)
		if scopeName == "" {
			scopeName = "VRT"
		}
		idx, exists := vrtIdx[scopeName]
		if !exists {
			idx = len(rows)
			vrtIdx[scopeName] = idx
			rows = append(rows, platform.RewardRow{Scope: scopeName})
		}
		switch strings.ToLower(strings.TrimSpace(v.Criticality)) {
		case "low":
			rows[idx].Low = rr
		case "medium":
			rows[idx].Medium = rr
		case "high":
			rows[idx].High = rr
		case "critical":
			rows[idx].Critical = rr
		}
	}
	return rows
}

func tierToRange(t *rewardTierJSON) platform.RewardRange {
	if t == nil {
		return platform.RewardRange{}
	}
	return makeRewardRange(t.From, t.To)
}

func makeRewardRange(from, to float64) platform.RewardRange {
	if to <= 0 && from > 0 {
		return platform.RewardRange{Max: from}
	}
	if to <= 0 {
		return platform.RewardRange{}
	}
	if from > 0 && from < to {
		return platform.RewardRange{Min: from, Max: to}
	}
	return platform.RewardRange{Max: to}
}

func rowHasReward(r platform.RewardRow) bool {
	return r.Low.Max > 0 || r.Medium.Max > 0 || r.High.Max > 0 || r.Critical.Max > 0
}
