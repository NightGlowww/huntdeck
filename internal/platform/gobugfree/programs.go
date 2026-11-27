package gobugfree

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
)

type engagementListJSON struct {
	Items []json.RawMessage `json:"items"`
}

type engagementItemJSON struct {
	ID             string   `json:"id"`
	OrgSlug        string   `json:"orgSlug"`
	OrgName        string   `json:"orgName"`
	Slug           string   `json:"slug"`
	Title          string   `json:"title"`
	Kind           string   `json:"kind"`
	Visibility     string   `json:"visibility"`
	Hidden         bool     `json:"hidden"`
	Status         string   `json:"status"`
	Summary        string   `json:"summary"`
	Industry       string   `json:"industry"`
	ScopeKinds     []string `json:"scopeKinds"`
	RewardCurrency string   `json:"rewardCurrency"`
	RewardMin      float64  `json:"rewardMin"`
	RewardMax      float64  `json:"rewardMax"`
	StartsOn       string   `json:"startsOn"`
	EndsOn         string   `json:"endsOn"`
	Member         bool     `json:"member"`
	Locked         bool     `json:"locked"`
	Invited        bool     `json:"invited"`
	InvitedAt      string   `json:"invitedAt"`
	UpdatedAt      string   `json:"updatedAt"`
}

// ListPrograms implements platform.Platform.
func (c *Client) ListPrograms(ctx context.Context) (platform.Listing, error) {
	var out platform.Listing
	auth := c.authenticated()
	if auth {
		if err := c.ensureSession(ctx); err != nil {
			return out, err
		}
		items, err := c.fetchAuthenticatedEngagements(ctx)
		out.Programs = c.convertEngagements(items, false)
		if err != nil {
			return out, err
		}
		out.Complete = true
		c.log.Info("gobugfree: listed programs", "mode", "authenticated", "total", len(out.Programs))
		return out, nil
	}

	var page engagementListJSON
	if err := c.getJSON(ctx, "/public/engagements", &page); err != nil {
		return out, fmt.Errorf("gobugfree: public engagements: %w", err)
	}
	out.Programs = c.convertEngagements(page.Items, true)
	out.Complete = true
	c.log.Info("gobugfree: listed programs", "mode", "public", "total", len(out.Programs))
	return out, nil
}

func (c *Client) fetchAuthenticatedEngagements(ctx context.Context) ([]json.RawMessage, error) {
	var all []json.RawMessage
	seenFirst := map[string]bool{}
	for pageNo := 1; ; pageNo++ {
		if pageNo > maxPages {
			return all, fmt.Errorf("gobugfree: engagements exceeded %d pages", maxPages)
		}
		var page engagementListJSON
		u := fmt.Sprintf("/engagements?page=%d", pageNo)
		if err := c.getJSON(ctx, u, &page); err != nil {
			return all, fmt.Errorf("gobugfree: engagements page %d: %w", pageNo, err)
		}
		if len(page.Items) == 0 {
			break
		}
		var first engagementItemJSON
		if json.Unmarshal(page.Items[0], &first) == nil {
			firstKey := firstNonEmpty(first.ID, makeHandle(first.OrgSlug, first.Slug))
			if firstKey != "" && seenFirst[firstKey] {
				break
			}
			if firstKey != "" {
				seenFirst[firstKey] = true
			}
		}
		all = append(all, page.Items...)
		if len(page.Items) < pageSize {
			break
		}
	}
	return all, nil
}

func (c *Client) convertEngagements(raws []json.RawMessage, public bool) []platform.Program {
	progs := make([]platform.Program, 0, len(raws))
	seen := make(map[string]bool, len(raws))
	for _, raw := range raws {
		var item engagementItemJSON
		if json.Unmarshal(raw, &item) != nil {
			continue
		}
		p, ok := c.toProgram(item, raw, public)
		if !ok || seen[p.Handle] {
			continue
		}
		seen[p.Handle] = true
		c.recordMeta(p.Handle, item)
		progs = append(progs, p)
	}
	return progs
}

func (c *Client) toProgram(item engagementItemJSON, raw json.RawMessage, public bool) (platform.Program, bool) {
	orgSlug := strings.TrimSpace(item.OrgSlug)
	slug := strings.TrimSpace(item.Slug)
	if orgSlug == "" || slug == "" {
		return platform.Program{}, false
	}
	handle := makeHandle(orgSlug, slug)
	name := firstNonEmpty(item.Title, item.OrgName, slug)

	vis := strings.ToLower(strings.TrimSpace(item.Visibility))
	private := item.Hidden || vis == "private" || vis == "invite_only"
	if public {
		private = false
	}

	offersBounty := item.RewardMax > 0 || strings.EqualFold(strings.TrimSpace(item.Kind), "bug_bounty")
	currency := strings.ToUpper(strings.TrimSpace(item.RewardCurrency))

	p := platform.Program{
		Handle:         handle,
		PlatformID:     makePlatformID(orgSlug, slug),
		Name:           name,
		URL:            c.engagementURL(orgSlug, slug, public),
		Private:        private,
		State:          mapProgramState(item.Status),
		OffersBounty:   offersBounty,
		Currency:       currency,
		Industry:       strings.TrimSpace(item.Industry),
		Policy:         strings.TrimSpace(item.Summary),
		StartedAt:      parseTime(item.StartsOn),
		ScopeChangedAt: parseTime(item.UpdatedAt),
		Raw:            raw,
	}
	if item.RewardMin > 0 {
		v := item.RewardMin
		p.BountyMin = &v
	}
	if item.RewardMax > 0 {
		v := item.RewardMax
		p.BountyMax = &v
	}
	return p, true
}

func (c *Client) recordMeta(handle string, item engagementItemJSON) {
	c.setMeta(handle, func(m *engagementMeta) {
		m.OrgSlug = strings.TrimSpace(item.OrgSlug)
		m.Slug = strings.TrimSpace(item.Slug)
		if item.Visibility != "" {
			m.Visibility = strings.ToLower(strings.TrimSpace(item.Visibility))
		}
		m.Invited = item.Invited
		m.Member = item.Member
		m.Locked = item.Locked
		if item.Title != "" {
			m.Title = strings.TrimSpace(item.Title)
		}
	})
}

func mapProgramState(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "paused":
		return platform.StatePaused
	case "closed", "ended", "archived":
		return platform.StateClosed
	case "active", "open", "":
		return platform.StateOpen
	default:
		return platform.StateOpen
	}
}
