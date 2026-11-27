package issuehunt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
)

const programsPageSize = 200

type pagedPrograms struct {
	Data    []json.RawMessage `json:"data"`
	Page    int               `json:"page"`
	PerPage int               `json:"perPage"`
	Total   int               `json:"total"`
}

type programEnvelope struct {
	Data json.RawMessage `json:"data"`
}

type organizationRefJSON struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	BountyApproved bool   `json:"bountyApproved"`
	Icon           string `json:"icon"`
}

type programContentJSON struct {
	Language    string `json:"language"`
	Description string `json:"description"`
	Guidelines  string `json:"guidelines"`
}

type rewardTierJSON struct {
	ID   string  `json:"id"`
	From float64 `json:"from"`
	To   float64 `json:"to"`
}

type criticalityRewardsJSON struct {
	Informational *rewardTierJSON `json:"informational"`
	Low           *rewardTierJSON `json:"low"`
	Medium        *rewardTierJSON `json:"medium"`
	High          *rewardTierJSON `json:"high"`
	Critical      *rewardTierJSON `json:"critical"`
}

type vrtRewardJSON struct {
	VRTID       string  `json:"vrtId"`
	Criticality string  `json:"criticality"`
	From        float64 `json:"from"`
	To          float64 `json:"to"`
}

type programRewardsJSON struct {
	CriticalityRewards *criticalityRewardsJSON `json:"criticalityRewards"`
	VRTs               []vrtRewardJSON         `json:"vrts"`
}

type scopeRuleJSON struct {
	ID          string `json:"id"`
	Criticality string `json:"criticality"`
	VRTID       string `json:"vrtId"`
}

type scopeGroupJSON struct {
	ID          string          `json:"id"`
	Type        []string        `json:"type"`
	Identifier  string          `json:"identifier"`
	Description string          `json:"description"`
	Scopes      []scopeRuleJSON `json:"scopes"`
}

type programJSON struct {
	ID             string               `json:"id"`
	Slug           string               `json:"slug"`
	Name           string               `json:"name"`
	Type           string               `json:"type"`
	Deleted        bool                 `json:"deleted"`
	Visibility     string               `json:"visibility"`
	Activated      bool                 `json:"activated"`
	Frozen         bool                 `json:"frozen"`
	ReportTemplate string               `json:"reportTemplate"`
	ReplySpeed     *float64             `json:"replySpeed"`
	CreditSystem   *string              `json:"creditSystem"`
	CVSSVersion    string               `json:"cvssVersion"`
	FirstActivated string               `json:"firstActivated"`
	Organization   *organizationRefJSON `json:"organization"`
	Contents       []programContentJSON `json:"contents"`
	Rewards        *programRewardsJSON  `json:"rewards"`
	ScopeGroups    []scopeGroupJSON     `json:"scopeGroups"`
}

// ListPrograms implements platform.Platform.
// It paginates GET /programs?perPage=200&page=1..N (never passing visibility=invite)
// and caches the full program structs for FetchScopeDetail.
func (c *Client) ListPrograms(ctx context.Context) (platform.Listing, error) {
	var out platform.Listing

	if c.authenticated() {
		if _, err := c.ensureAuth(ctx); err != nil {
			return out, err
		}
	}

	seen := map[string]bool{}
	for pageNo := 1; ; pageNo++ {
		if pageNo > maxPages {
			return out, fmt.Errorf("issuehunt: list programs exceeded %d pages", maxPages)
		}
		var page pagedPrograms
		path := fmt.Sprintf("/programs?perPage=%d&page=%d", programsPageSize, pageNo)
		if err := c.getJSON(ctx, path, &page); err != nil {
			return out, fmt.Errorf("issuehunt: list programs page %d: %w", pageNo, err)
		}

		for _, raw := range page.Data {
			var pj programJSON
			if err := json.Unmarshal(raw, &pj); err != nil {
				c.log.Warn("issuehunt: skipping malformed program item", "page", pageNo, "err", err)
				continue
			}
			prog, ok := c.convertProgram(pj, raw)
			if !ok || seen[prog.Handle] {
				continue
			}
			seen[prog.Handle] = true
			c.cacheProgram(prog.Handle, pj)
			out.Programs = append(out.Programs, prog)
		}

		if len(page.Data) == 0 {
			break
		}
		perPage := page.PerPage
		if perPage <= 0 {
			perPage = programsPageSize
		}
		if page.Total > 0 {
			if (pageNo-1)*perPage+len(page.Data) >= page.Total {
				break
			}
		} else if len(page.Data) < perPage {
			break
		}
	}

	if c.authenticated() {
		c.cred.Accepted()
	}
	out.Complete = true
	c.log.Info("issuehunt: listed programs", "total", len(out.Programs), "complete", out.Complete)
	return out, nil
}

func (c *Client) cacheProgram(handle string, pj programJSON) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	if handle != "" {
		c.programs[handle] = pj
	}
	if id := strings.TrimSpace(pj.ID); id != "" {
		c.programs[id] = pj
	}
}

func (c *Client) cachedProgram(handle, platformID string) (programJSON, bool) {
	c.cacheMu.RLock()
	defer c.cacheMu.RUnlock()
	if handle != "" {
		if pj, ok := c.programs[handle]; ok {
			return pj, true
		}
	}
	if platformID != "" {
		if pj, ok := c.programs[platformID]; ok {
			return pj, true
		}
	}
	return programJSON{}, false
}

func (c *Client) fetchProgramDetail(ctx context.Context, slugOrID string) (programJSON, error) {
	var env programEnvelope
	path := "/programs/" + url.PathEscape(slugOrID)
	if err := c.getJSON(ctx, path, &env); err != nil {
		return programJSON{}, err
	}
	var pj programJSON
	if err := json.Unmarshal(env.Data, &pj); err != nil {
		return programJSON{}, fmt.Errorf("issuehunt: decode program %s: %w", slugOrID, err)
	}
	handle := firstNonEmpty(pj.Slug, pj.ID, slugOrID)
	c.cacheProgram(handle, pj)
	return pj, nil
}

func (c *Client) convertProgram(pj programJSON, raw json.RawMessage) (platform.Program, bool) {
	handle := firstNonEmpty(pj.Slug, pj.ID)
	if handle == "" {
		return platform.Program{}, false
	}
	platformID := firstNonEmpty(pj.ID, handle)
	name := firstNonEmpty(pj.Name, handle)
	webURL := strings.TrimSuffix(c.web.String(), "/") + "/programs/" + url.PathEscape(handle)

	p := platform.Program{
		Handle:     handle,
		PlatformID: platformID,
		Name:       name,
		URL:        webURL,
		Private:    isPrivateVisibility(pj.Visibility),
		State:      mapProgramState(pj),
		StartedAt:  parseTime(pj.FirstActivated),
		Policy:     extractPolicy(pj.Contents),
		Requirements: &platform.Requirements{
			Application: pj.Visibility == "application",
		},
		Raw: raw,
	}

	if programOffersBounty(pj) {
		p.OffersBounty = true
		p.Currency = currencyJPY
		lo, hi := rewardMinMax(pj.Rewards)
		if hi > 0 {
			p.BountyMax = &hi
			if lo > 0 {
				p.BountyMin = &lo
			}
		}
	}

	return p, true
}

func isPrivateVisibility(visibility string) bool {
	switch strings.ToLower(strings.TrimSpace(visibility)) {
	case "public", "application", "":
		return false
	default:
		return true
	}
}

func mapProgramState(pj programJSON) string {
	if !pj.Activated || pj.Deleted {
		return platform.StateClosed
	}
	if pj.Frozen {
		return platform.StatePaused
	}
	return platform.StateOpen
}

func isPointCreditSystem(pj programJSON) bool {
	return pj.CreditSystem != nil && strings.EqualFold(strings.TrimSpace(*pj.CreditSystem), "point")
}

func programOffersBounty(pj programJSON) bool {
	if isPointCreditSystem(pj) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(pj.Type), "vdp") {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(pj.Type), "bounty") {
		return true
	}
	_, hi := rewardMinMax(pj.Rewards)
	return hi > 0
}

func rewardMinMax(rewards *programRewardsJSON) (lo, hi float64) {
	if rewards == nil {
		return 0, 0
	}
	consider := func(v float64) {
		if v <= 0 {
			return
		}
		if lo == 0 || v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	if cr := rewards.CriticalityRewards; cr != nil {
		for _, t := range []*rewardTierJSON{cr.Informational, cr.Low, cr.Medium, cr.High, cr.Critical} {
			if t != nil {
				consider(t.From)
				consider(t.To)
			}
		}
	}
	for _, v := range rewards.VRTs {
		consider(v.From)
		consider(v.To)
	}
	return lo, hi
}

func extractPolicy(contents []programContentJSON) string {
	if len(contents) == 0 {
		return ""
	}
	format := func(c programContentJSON) string {
		var parts []string
		if d := strings.TrimSpace(c.Description); d != "" {
			parts = append(parts, d)
		}
		if g := strings.TrimSpace(c.Guidelines); g != "" {
			parts = append(parts, g)
		}
		return strings.Join(parts, "\n\n")
	}
	for _, lang := range []string{"en", "jp", "ja"} {
		for _, c := range contents {
			if strings.EqualFold(strings.TrimSpace(c.Language), lang) {
				if md := format(c); md != "" {
					return md
				}
			}
		}
	}
	for _, c := range contents {
		if md := format(c); md != "" {
			return md
		}
	}
	return ""
}
