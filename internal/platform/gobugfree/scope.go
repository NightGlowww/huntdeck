package gobugfree

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

type engagementDetailJSON struct {
	OrgSlug                   string            `json:"orgSlug"`
	OrgName                   string            `json:"orgName"`
	OrgAbout                  string            `json:"orgAbout"`
	Slug                      string            `json:"slug"`
	Title                     string            `json:"title"`
	Kind                      string            `json:"kind"`
	Status                    string            `json:"status"`
	Summary                   string            `json:"summary"`
	RequiredVerificationLevel string            `json:"requiredVerificationLevel"`
	Locked                    bool              `json:"locked"`
	BriefingAccessible        bool              `json:"briefingAccessible"`
	NDARequired               bool              `json:"ndaRequired"`
	NDAAccepted               bool              `json:"ndaAccepted"`
	RulesAccepted             bool              `json:"rulesAccepted"`
	StartsOn                  string            `json:"startsOn"`
	EndsOn                    string            `json:"endsOn"`
	UpdatedAt                 string            `json:"updatedAt"`
	Scope                     []scopeEntryJSON  `json:"scope"`
	Bounty                    []bountyEntryJSON `json:"bounty"`
	Program                   programBodyJSON   `json:"program"`
	Leaderboard               []leaderboardJSON `json:"leaderboard"`
}

type scopeEntryJSON struct {
	ScopeEntryID string `json:"scopeEntryId"`
	Pattern      string `json:"pattern"`
	Kind         string `json:"kind"`
	InScope      bool   `json:"inScope"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
}

type bountyEntryJSON struct {
	Severity string  `json:"severity"`
	Min      float64 `json:"min"`
	Max      float64 `json:"max"`
	Currency string  `json:"currency"`
	Text     string  `json:"text"`
}

type programBodyJSON struct {
	Summary           string   `json:"summary"`
	ScopeNotes        string   `json:"scopeNotes"`
	RulesText         string   `json:"rulesText"`
	TestingAllowed    []string `json:"testingAllowed"`
	TestingDisallowed []string `json:"testingDisallowed"`
	QualifiedText     string   `json:"qualifiedText"`
	QualifiedVulns    []string `json:"qualifiedVulns"`
	NonQualifiedText  string   `json:"nonQualifiedText"`
	NonQualifiedVulns []string `json:"nonQualifiedVulns"`
	Procedure         []string `json:"procedure"`
	Notes             string   `json:"notes"`
	SafeHarbor        string   `json:"safeHarbor"`
}

type leaderboardJSON struct {
	Rank   int    `json:"rank"`
	Handle string `json:"handle"`
	Points int    `json:"points"`
}

// FetchScope implements platform.Platform.
func (c *Client) FetchScope(ctx context.Context, p platform.Program) ([]platform.ScopeEntry, error) {
	entries, _, err := c.FetchScopeDetail(ctx, p)
	return entries, err
}

// FetchScopeDetail implements platform.ScopeDetailFetcher.
func (c *Client) FetchScopeDetail(ctx context.Context, p platform.Program) ([]platform.ScopeEntry, platform.ScopeDetail, error) {
	orgSlug, slug, err := c.resolveOrgSlug(p)
	if err != nil {
		return nil, platform.ScopeDetail{}, err
	}
	if c.authenticated() {
		if err := c.ensureSession(ctx); err != nil {
			return nil, platform.ScopeDetail{}, err
		}
	}

	d, err := c.fetchEngagementDetail(ctx, orgSlug, slug)
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == http.StatusForbidden {
			return nil, platform.ScopeDetail{}, fmt.Errorf("gobugfree: engagement %s access forbidden: %w", p.Handle, platform.ErrScopeUnavailable)
		}
		return nil, platform.ScopeDetail{}, err
	}

	detail := buildScopeDetail(d)
	if d.Locked && !d.BriefingAccessible {
		return nil, detail, fmt.Errorf("gobugfree: engagement %s is locked: %w", p.Handle, platform.ErrScopeUnavailable)
	}

	offersBounty := len(detail.Rewards) > 0 || strings.EqualFold(strings.TrimSpace(d.Kind), "bug_bounty")
	entries := convertScopeEntries(d.Scope, offersBounty)
	return entries, detail, nil
}

func (c *Client) fetchEngagementDetail(ctx context.Context, orgSlug, slug string) (engagementDetailJSON, error) {
	prefix := "/public/engagements/"
	if c.authenticated() {
		prefix = "/engagements/"
	}
	path := prefix + url.PathEscape(orgSlug) + "/" + url.PathEscape(slug)
	var d engagementDetailJSON
	if err := c.getJSON(ctx, path, &d); err != nil {
		return engagementDetailJSON{}, fmt.Errorf("gobugfree: engagement %s/%s detail: %w", orgSlug, slug, err)
	}
	return d, nil
}

var (
	// Matches descriptive label prefixes like "Website: example.com" or "Mobile App: com.example.app",
	// never matching URL schemes like "https://...".
	prefixLabelRE = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9 _/-]{1,30}):\s+(.+)$`)
	// Matches trailing parenthetical notes separated by whitespace, e.g. "id.example.com (only misconfigurations)",
	// preserving inline alternations like "www.example.(com|co.uk)".
	trailingNoteRE = regexp.MustCompile(`^(.*?)\s+\(([^()]+)\)$`)
)

// cleanScopePattern strips leading descriptive prefixes (e.g. "Website: ") and
// trailing parenthetical notes (e.g. " (only misconfigurations)") so the target
// parses cleanly with internal/scope while preserving notes for Instruction.
func cleanScopePattern(raw string) (cleaned, prefixNote, parenNote string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "", ""
	}
	if m := prefixLabelRE.FindStringSubmatch(s); m != nil {
		label := strings.TrimSpace(m[1])
		low := strings.ToLower(label)
		if low != "http" && low != "https" && low != "ftp" {
			prefixNote = label
			s = strings.TrimSpace(m[2])
		}
	}
	if m := trailingNoteRE.FindStringSubmatch(s); m != nil {
		cand := strings.TrimSpace(m[1])
		if cand != "" {
			cleaned = cand
			parenNote = strings.TrimSpace(m[2])
			return cleaned, prefixNote, parenNote
		}
	}
	return s, prefixNote, parenNote
}

func convertScopeEntries(items []scopeEntryJSON, offersBounty bool) []platform.ScopeEntry {
	entries := make([]platform.ScopeEntry, 0, len(items))
	seen := make(map[string]bool, len(items))

	add := func(e platform.ScopeEntry) {
		key := fmt.Sprintf("%t|%s|%s", e.InScope, e.AssetType, strings.ToLower(strings.TrimSpace(e.Identifier)))
		if e.Identifier == "" || seen[key] {
			return
		}
		seen[key] = true
		entries = append(entries, e)
	}

	for _, item := range items {
		cleaned, prefixNote, parenNote := cleanScopePattern(item.Pattern)
		if cleaned == "" {
			continue
		}
		instr := joinNotes(prefixNote, parenNote, item.Description, item.Instructions)
		assetType := normalizeAssetType(item.Kind, cleaned)
		eligible := item.InScope && offersBounty

		targets := scope.Targets(assetType, cleaned)
		if len(targets) > 0 {
			for i, t := range targets {
				entryType := t.Kind
				if assetType == scope.AssetAPI && (t.Kind == scope.KindDomain || t.Kind == scope.KindURL) {
					entryType = scope.AssetAPI
				}
				pid := item.ScopeEntryID
				if i > 0 && pid != "" {
					pid = fmt.Sprintf("%s#%d", pid, i+1)
				}
				add(platform.ScopeEntry{
					PlatformID:        pid,
					InScope:           item.InScope,
					AssetType:         entryType,
					PlatformAssetType: strings.TrimSpace(item.Kind),
					Identifier:        t.Value,
					BountyEligible:    eligible,
					Instruction:       instr,
				})
			}
			continue
		}

		add(platform.ScopeEntry{
			PlatformID:        item.ScopeEntryID,
			InScope:           item.InScope,
			AssetType:         assetType,
			PlatformAssetType: strings.TrimSpace(item.Kind),
			Identifier:        cleaned,
			BountyEligible:    eligible,
			Instruction:       instr,
		})
	}
	return entries
}

func joinNotes(notes ...string) string {
	var out []string
	for _, n := range notes {
		n = strings.TrimSpace(n)
		if n == "" || slices.Contains(out, n) {
			continue
		}
		out = append(out, n)
	}
	return strings.Join(out, " — ")
}

func normalizeAssetType(kind, ident string) string {
	low := strings.ToLower(strings.TrimSpace(kind))
	switch low {
	case "api":
		return scope.AssetAPI
	case "android":
		return scope.AssetAndroid
	case "ios":
		return scope.AssetIOS
	case "mobile":
		lowIdent := strings.ToLower(ident)
		switch {
		case strings.Contains(lowIdent, "apple.com") || strings.Contains(lowIdent, "ios"):
			return scope.AssetIOS
		case strings.Contains(lowIdent, "play.google") || strings.Contains(lowIdent, "android"):
			return scope.AssetAndroid
		case scope.WebType(ident) != scope.AssetOther:
			return scope.WebType(ident)
		default:
			return scope.AssetOther
		}
	case "ip", "cidr", "ip_range", "network":
		if _, err := netip.ParsePrefix(ident); err == nil {
			return scope.AssetCIDR
		}
		if _, err := netip.ParseAddr(ident); err == nil {
			return scope.AssetIP
		}
		return scope.AssetIP
	case "source_code", "code", "repository", "github":
		return scope.AssetSourceCode
	case "hardware", "iot":
		return scope.AssetHardware
	case "executable", "desktop", "windows", "macos", "client":
		return scope.AssetExecutable
	case "smart_contract", "blockchain":
		return scope.AssetSmartContract
	case "ai", "ai_model":
		return scope.AssetAIModel
	case "web", "website", "domain", "url", "":
		if _, err := netip.ParsePrefix(ident); err == nil {
			return scope.AssetCIDR
		}
		if _, err := netip.ParseAddr(ident); err == nil {
			return scope.AssetIP
		}
		return scope.WebType(ident)
	default:
		if _, err := netip.ParsePrefix(ident); err == nil {
			return scope.AssetCIDR
		}
		if _, err := netip.ParseAddr(ident); err == nil {
			return scope.AssetIP
		}
		return scope.WebType(ident)
	}
}

func buildScopeDetail(d engagementDetailJSON) platform.ScopeDetail {
	rewards := buildRewardRows(d.Bounty)
	state := ""
	if strings.TrimSpace(d.Status) != "" {
		state = mapProgramState(d.Status)
	}
	return platform.ScopeDetail{
		Policy:       buildPolicy(d),
		Rewards:      rewards,
		Requirements: buildRequirements(d),
		Facts:        buildFacts(d, rewards),
		State:        state,
	}
}

func buildPolicy(d engagementDetailJSON) string {
	var parts []string
	addText := func(heading, body string) {
		body = strings.TrimSpace(body)
		if body == "" {
			return
		}
		if heading != "" {
			parts = append(parts, heading+"\n\n"+body)
			return
		}
		parts = append(parts, body)
	}
	addBullets := func(heading, intro string, items []string) {
		var bullets []string
		for _, it := range items {
			if it = strings.TrimSpace(it); it != "" {
				bullets = append(bullets, "- "+it)
			}
		}
		intro = strings.TrimSpace(intro)
		if intro == "" && len(bullets) == 0 {
			return
		}
		var sec []string
		if intro != "" {
			sec = append(sec, intro)
		}
		if len(bullets) > 0 {
			sec = append(sec, strings.Join(bullets, "\n"))
		}
		parts = append(parts, heading+"\n\n"+strings.Join(sec, "\n\n"))
	}

	summary := firstNonEmpty(d.Program.Summary, d.Summary)
	addText("", summary)
	addText("## Scope Notes", d.Program.ScopeNotes)
	addText("## Rules", d.Program.RulesText)
	addBullets("## Testing Allowed", "", d.Program.TestingAllowed)
	addBullets("## Testing Disallowed", "", d.Program.TestingDisallowed)
	addBullets("## Qualified Vulnerabilities", d.Program.QualifiedText, d.Program.QualifiedVulns)
	addBullets("## Non-Qualified Vulnerabilities", d.Program.NonQualifiedText, d.Program.NonQualifiedVulns)
	addBullets("## Procedure", "", d.Program.Procedure)
	addText("## Notes", d.Program.Notes)
	addText("## Safe Harbor", d.Program.SafeHarbor)

	return strings.Join(parts, "\n\n")
}

func buildRewardRows(entries []bountyEntryJSON) []platform.RewardRow {
	if len(entries) == 0 {
		return nil
	}
	row := platform.RewardRow{Scope: "Default"}
	hasAny := false
	for _, b := range entries {
		rng := makeRewardRange(b.Min, b.Max)
		if rng.Max <= 0 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(b.Severity)) {
		case "low":
			row.Low = rng
			hasAny = true
		case "medium":
			row.Medium = rng
			hasAny = true
		case "high":
			row.High = rng
			hasAny = true
		case "critical":
			row.Critical = rng
			hasAny = true
		}
	}
	if !hasAny {
		return nil
	}
	return []platform.RewardRow{row}
}

func makeRewardRange(lo, hi float64) platform.RewardRange {
	if hi <= 0 && lo > 0 {
		return platform.RewardRange{Max: lo}
	}
	if hi <= 0 {
		return platform.RewardRange{}
	}
	if lo > 0 && lo < hi {
		return platform.RewardRange{Min: lo, Max: hi}
	}
	return platform.RewardRange{Max: hi}
}

func buildRequirements(d engagementDetailJSON) *platform.Requirements {
	req := &platform.Requirements{
		Terms: d.NDARequired,
	}
	switch strings.ToLower(strings.TrimSpace(d.RequiredVerificationLevel)) {
	case "identity":
		req.IDVerified = true
	case "background_check":
		req.IDVerified = true
		req.Cleared = true
	}
	return req
}

func buildFacts(d engagementDetailJSON, rewards []platform.RewardRow) *platform.Facts {
	var f platform.Facts
	has := false

	if strings.TrimSpace(d.Program.SafeHarbor) != "" {
		f.SafeHarbor = "full"
		has = true
	}
	if len(rewards) > 0 {
		yes := true
		f.OffersBounty = &yes
		has = true
	} else if strings.EqualFold(strings.TrimSpace(d.Kind), "vdp") {
		no := false
		f.OffersBounty = &no
		has = true
	}
	if st := parseTime(d.StartsOn); st != nil {
		f.StartedAt = st
		has = true
	}
	if !has {
		return nil
	}
	return &f
}
