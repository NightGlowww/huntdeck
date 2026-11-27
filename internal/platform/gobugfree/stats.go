package gobugfree

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
)

const statsBatch = 25

// FetchProgramStats implements platform.StatsFetcher.
// It reads engagement detail JSON for each handle in batches of 25, populating
// leaderboard counts (HackersThanked, Hunters), Rewards, Profile, Facts, and
// Requirements. Missing or restricted programs are skipped; auth or context
// errors stop the run.
func (c *Client) FetchProgramStats(ctx context.Context, handles []string, save platform.StatsSaver) error {
	if c.authenticated() {
		if err := c.ensureSession(ctx); err != nil {
			return fmt.Errorf("gobugfree: program stats: %w", err)
		}
	}

	for start := 0; start < len(handles); start += statsBatch {
		batch := handles[start:min(start+statsBatch, len(handles))]
		out := make(map[string]platform.ProgramStats, len(batch))
		for _, h := range batch {
			orgSlug, slug, err := c.resolveOrgSlug(platform.Program{Handle: h})
			if err != nil {
				continue
			}
			d, err := c.fetchEngagementDetail(ctx, orgSlug, slug)
			if err != nil {
				if errors.Is(err, ErrSessionExpired) || ctx.Err() != nil {
					return fmt.Errorf("gobugfree: program stats for %s: %w", h, err)
				}
				continue
			}
			if s, ok := convertProgramStats(d); ok {
				out[h] = s
			}
		}
		if err := save(batch, out); err != nil {
			return err
		}
	}
	return nil
}

func convertProgramStats(d engagementDetailJSON) (platform.ProgramStats, bool) {
	rewards := buildRewardRows(d.Bounty)
	cur := ""
	for _, b := range d.Bounty {
		if c := strings.ToUpper(strings.TrimSpace(b.Currency)); c != "" {
			cur = c
			break
		}
	}

	s := platform.ProgramStats{
		Currency:     cur,
		Rewards:      rewards,
		Requirements: buildRequirements(d),
		Facts:        buildFacts(d, rewards),
		Profile:      buildProfile(d),
	}

	if n := len(d.Leaderboard); n > 0 {
		s.HackersThanked = &n
		s.Hunters = &n
	}

	hasData := s.HackersThanked != nil ||
		len(s.Rewards) > 0 ||
		s.Facts != nil ||
		s.Requirements.IDVerified || s.Requirements.Cleared || s.Requirements.Terms ||
		s.Profile.Introduction != "" ||
		s.Profile.InScope != "" ||
		s.Profile.OutScope != "" ||
		len(s.Profile.ScopeExclusions) > 0 ||
		s.Profile.FAQ != ""

	return s, hasData
}

func buildProfile(d engagementDetailJSON) platform.ProgramProfile {
	p := platform.ProgramProfile{
		Introduction: firstNonEmpty(d.OrgAbout, d.Program.Summary, d.Summary),
	}

	var inParts []string
	if t := strings.TrimSpace(d.Program.QualifiedText); t != "" {
		inParts = append(inParts, t)
	}
	if b := formatBullets(d.Program.QualifiedVulns); b != "" {
		inParts = append(inParts, b)
	}
	if b := formatBullets(d.Program.TestingAllowed); b != "" {
		inParts = append(inParts, "### Testing Allowed\n\n"+b)
	}
	p.InScope = strings.Join(inParts, "\n\n")

	var outParts []string
	if t := strings.TrimSpace(d.Program.ScopeNotes); t != "" {
		outParts = append(outParts, t)
	}
	if t := strings.TrimSpace(d.Program.NonQualifiedText); t != "" {
		outParts = append(outParts, t)
	}
	if b := formatBullets(d.Program.NonQualifiedVulns); b != "" {
		outParts = append(outParts, b)
	}
	if b := formatBullets(d.Program.TestingDisallowed); b != "" {
		outParts = append(outParts, "### Testing Disallowed\n\n"+b)
	}
	p.OutScope = strings.Join(outParts, "\n\n")

	for _, item := range d.Program.TestingDisallowed {
		if item = strings.TrimSpace(item); item != "" {
			p.ScopeExclusions = append(p.ScopeExclusions, platform.Exclusion{Name: item})
		}
	}

	var faqParts []string
	if b := formatBullets(d.Program.Procedure); b != "" {
		faqParts = append(faqParts, "### Procedure\n\n"+b)
	}
	if t := strings.TrimSpace(d.Program.Notes); t != "" {
		faqParts = append(faqParts, t)
	}
	p.FAQ = strings.Join(faqParts, "\n\n")

	return p
}

func formatBullets(items []string) string {
	var out []string
	for _, it := range items {
		if it = strings.TrimSpace(it); it != "" {
			out = append(out, "- "+it)
		}
	}
	return strings.Join(out, "\n")
}
