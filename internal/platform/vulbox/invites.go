package vulbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

type centerInviteItem struct {
	ID            flexInt64       `json:"id"`
	ProjectID     flexInt64       `json:"project_id"`
	TaskID        flexInt64       `json:"task_id"`
	InviteCode    string          `json:"invite_code"`
	TakeMargin    flexBool        `json:"take_margin"`
	IsMarginWhite flexBool        `json:"is_margin_white"`
	CreatedAt     json.RawMessage `json:"created_at"`
	InvitedAt     json.RawMessage `json:"invited_at"`
}

func (ci centerInviteItem) effectiveID() int64 {
	if ci.ProjectID.Int64() > 0 {
		return ci.ProjectID.Int64()
	}
	if ci.TaskID.Int64() > 0 {
		return ci.TaskID.Int64()
	}
	return ci.ID.Int64()
}

// ListInvites implements platform.InviteLister.
// It queries both /hacker/center/invitation and /hacker/project/mine/list?status=1
// and deduplicates pending project invitations by project ID.
func (c *Client) ListInvites(ctx context.Context) ([]platform.Invite, error) {
	if !c.authenticated() {
		return nil, platform.ErrNoWebLogin
	}
	if err := c.ensureSession(ctx); err != nil {
		return nil, fmt.Errorf("vulbox: list invites: %w", err)
	}

	seen := map[int64]platform.Invite{}
	order := []int64{}
	add := func(id int64, invitedAt *time.Time, code string, takeMargin, marginWhite bool) {
		if id <= 0 {
			return
		}
		handle := strconv.FormatInt(id, 10)
		c.setMeta(handle, func(m *projectMeta) {
			m.ID = id
			m.IsInvited = true
			if code != "" {
				m.InviteCode = code
			}
			if takeMargin {
				m.TakeMargin = true
			}
			if marginWhite {
				m.IsMarginWhite = true
			}
		})
		if cur, ok := seen[id]; ok {
			if cur.InvitedAt.IsZero() && invitedAt != nil {
				cur.InvitedAt = *invitedAt
				seen[id] = cur
			}
			return
		}
		inv := platform.Invite{Handle: handle}
		if invitedAt != nil {
			inv.InvitedAt = *invitedAt
		}
		seen[id] = inv
		order = append(order, id)
	}

	// 1. Dashboard pending invitations endpoint (/hacker/center/invitation).
	// Often returns {} when empty; handle array, {list:[]}, {data:[]}, or single object.
	var centerRaw json.RawMessage
	if err := c.getJSON(ctx, "/hacker/center/invitation", nil, &centerRaw); err != nil {
		if errors.Is(err, ErrSessionExpired) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("vulbox: list invites: %w", err)
		}
		c.log.Debug("vulbox: center/invitation failed, continuing with mine/list", "err", err)
	} else {
		for _, ci := range parseCenterInvites(centerRaw) {
			at := parseCSTTime(ci.InvitedAt)
			if at == nil {
				at = parseCSTTime(ci.CreatedAt)
			}
			add(ci.effectiveID(), at, ci.InviteCode, ci.TakeMargin.Bool(), ci.IsMarginWhite.Bool())
		}
	}

	// 2. My Projects filtered by status=1 (邀请中 / pending invitation).
	pages, err := c.fetchProjectPages(ctx, "/hacker/project/mine/list?type=-1&status=1&project_status=-1&page=%d&page_size=50", "mine/invites")
	if err != nil {
		return nil, fmt.Errorf("vulbox: list invites: %w", err)
	}
	for _, raw := range pages {
		var item projectItemJSON
		if json.Unmarshal(raw, &item) != nil {
			continue
		}
		id := item.effectiveID()
		if id <= 0 {
			continue
		}
		add(id, parseCSTTime(item.TaskSTime), item.InviteCode, item.TakeMargin.Bool(), item.IsMarginWhite.Bool())
	}

	out := make([]platform.Invite, 0, len(order))
	for _, id := range order {
		out = append(out, seen[id])
	}
	return out, nil
}

// AcceptInvite implements platform.InviteAccepter.
// It checks whether the project requires a cash margin deposit before posting
// POST /hacker/project/invite (status: 1) via the non-retrying action client.
func (c *Client) AcceptInvite(ctx context.Context, handle string) error {
	if !c.authenticated() {
		return platform.ErrNoWebLogin
	}
	if err := c.ensureSession(ctx); err != nil {
		return fmt.Errorf("vulbox: accept invite %s: %w", handle, err)
	}
	projectID, err := parseProjectID(handle, "")
	if err != nil {
		return fmt.Errorf("vulbox: accept invite: %w", err)
	}

	meta, hasMeta := c.getMeta(handle)
	if hasMeta && meta.TakeMargin && !meta.IsMarginWhite {
		return fmt.Errorf("vulbox: accept invite %s: project requires a cash margin deposit (take_margin=1); accept manually on VulBox", handle)
	}

	// Verify live detail to catch margin-gated projects even when AcceptInvite
	// is called before a listing cached projectMeta.
	detail, err := c.fetchProjectDetail(ctx, projectID)
	if err != nil {
		if errors.Is(err, ErrSessionExpired) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("vulbox: accept invite %s: %w", handle, err)
		}
	} else if (detail.TakeMargin.Bool() || detail.MarginAmount.Float64() > 0) && !detail.IsMarginWhite.Bool() {
		return fmt.Errorf("vulbox: accept invite %s: project requires a cash margin deposit (take_margin=1); accept manually on VulBox", handle)
	}

	if err := c.postInviteAccept(ctx, projectID, meta.InviteCode); err != nil {
		if isAPICode(err, 400001) {
			return fmt.Errorf("vulbox: accept invite %s: insufficient margin deposit balance (code 400001): %w", handle, err)
		}
		return fmt.Errorf("vulbox: accept invite %s: %w", handle, err)
	}

	c.setMeta(handle, func(m *projectMeta) {
		m.ID = projectID
		m.IsInvited = false
		m.IsJoined = true
	})
	return nil
}

func (c *Client) postInviteAccept(ctx context.Context, projectID int64, inviteCode string) error {
	body := struct {
		ProjectID  int64  `json:"project_id"`
		Status     int    `json:"status"`
		InviteCode string `json:"invite_code,omitempty"`
	}{
		ProjectID:  projectID,
		Status:     1,
		InviteCode: strings.TrimSpace(inviteCode),
	}
	return c.postJSON(ctx, "/hacker/project/invite", body, nil, nil)
}

func parseCenterInvites(raw json.RawMessage) []centerInviteItem {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" || s == "{}" || s == "[]" {
		return nil
	}
	var slice []centerInviteItem
	if json.Unmarshal(raw, &slice) == nil {
		return slice
	}
	var wrapped struct {
		List []centerInviteItem `json:"list"`
		Data []centerInviteItem `json:"data"`
	}
	if json.Unmarshal(raw, &wrapped) == nil {
		if len(wrapped.List) > 0 {
			return wrapped.List
		}
		if len(wrapped.Data) > 0 {
			return wrapped.Data
		}
	}
	var single centerInviteItem
	if json.Unmarshal(raw, &single) == nil && single.effectiveID() > 0 {
		return []centerInviteItem{single}
	}
	return nil
}
