package gobugfree

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// ListInvites implements platform.InviteLister.
// Pending invitations appear on GET /engagements with invited=true && member=false.
func (c *Client) ListInvites(ctx context.Context) ([]platform.Invite, error) {
	if !c.authenticated() {
		return nil, platform.ErrNoWebLogin
	}
	if err := c.ensureSession(ctx); err != nil {
		return nil, fmt.Errorf("gobugfree: list invites: %w", err)
	}

	raws, err := c.fetchAuthenticatedEngagements(ctx)
	if err != nil {
		return nil, fmt.Errorf("gobugfree: list invites: %w", err)
	}

	var invites []platform.Invite
	seen := map[string]bool{}
	for _, raw := range raws {
		var item engagementItemJSON
		if json.Unmarshal(raw, &item) != nil {
			continue
		}
		if item.OrgSlug == "" || item.Slug == "" {
			continue
		}
		handle := makeHandle(item.OrgSlug, item.Slug)
		c.recordMeta(handle, item)
		if !item.Invited || item.Member || seen[handle] {
			continue
		}
		seen[handle] = true
		inv := platform.Invite{Handle: handle}
		if t := parseTime(item.InvitedAt); t != nil {
			inv.InvitedAt = *t
		}
		invites = append(invites, inv)
	}
	return invites, nil
}

// AcceptInvite implements platform.InviteAccepter.
// Calls POST /engagements/{org_slug}/{engagement_slug}/invitation/accept using
// the non-retrying action client.
func (c *Client) AcceptInvite(ctx context.Context, handle string) error {
	if !c.authenticated() {
		return platform.ErrNoWebLogin
	}
	if err := c.ensureSession(ctx); err != nil {
		return fmt.Errorf("gobugfree: accept invite %s: %w", handle, err)
	}
	orgSlug, slug, err := c.resolveOrgSlug(platform.Program{Handle: handle})
	if err != nil {
		return fmt.Errorf("gobugfree: accept invite: %w", err)
	}

	path := "/engagements/" + url.PathEscape(orgSlug) + "/" + url.PathEscape(slug) + "/invitation/accept"
	var resp struct {
		State string `json:"state"`
	}
	if err := c.postJSON(ctx, path, nil, &resp); err != nil {
		return fmt.Errorf("gobugfree: accept invite %s: %w", handle, err)
	}

	c.setMeta(handle, func(m *engagementMeta) {
		m.OrgSlug = orgSlug
		m.Slug = slug
		m.Invited = false
		m.Member = true
		m.Locked = false
	})
	return nil
}
