package vulbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// pagedProjects is Laravel-style pagination for project listings.
type pagedProjects struct {
	CurrentPage int               `json:"current_page"`
	LastPage    int               `json:"last_page"`
	PerPage     int               `json:"per_page"`
	Total       int               `json:"total"`
	Data        []json.RawMessage `json:"data"`
}

type bonusRangeJSON struct {
	LowMin     flexFloat `json:"low_min"`
	LowMax     flexFloat `json:"low_max"`
	MiddleMin  flexFloat `json:"middle_min"`
	MiddleMax  flexFloat `json:"middle_max"`
	HighMin    flexFloat `json:"high_min"`
	HighMax    flexFloat `json:"high_max"`
	SeriousMin flexFloat `json:"serious_min"`
	SeriousMax flexFloat `json:"serious_max"`
}

// projectItemJSON covers items from /hacker/project/mine/list,
// /hacker/project/projects, /web/project/projects, and /web/project/enterprise/src.
type projectItemJSON struct {
	ID              flexInt64       `json:"id"`
	ProjectID       flexInt64       `json:"project_id"`
	Type            int             `json:"type"`
	ProjectType     int             `json:"project_type"`
	Status          int             `json:"status"`
	TaskStatus      *int            `json:"task_status"`
	ProjectStatus   *int            `json:"project_status"`
	TaskTitle       string          `json:"task_title"`
	TaskDesc        string          `json:"task_desc"`
	TaskIsSRC       flexBool        `json:"task_is_src"`
	TaskWebsite     string          `json:"task_website"`
	TaskBonusLevel  int             `json:"task_bonus_level"`
	TaskSTime       json.RawMessage `json:"task_stime"`
	TaskETime       json.RawMessage `json:"task_etime"`
	IsStaticTask    flexBool        `json:"is_static_task"`
	Goto            string          `json:"goto"`
	SRCURL          string          `json:"src_url"`
	RewardRange     string          `json:"reward_range"`
	BonusRange      *bonusRangeJSON `json:"bonus_range"`
	LowMin          flexFloat       `json:"low_min"`
	LowMax          flexFloat       `json:"low_max"`
	MiddleMin       flexFloat       `json:"middle_min"`
	MiddleMax       flexFloat       `json:"middle_max"`
	HighMin         flexFloat       `json:"high_min"`
	HighMax         flexFloat       `json:"high_max"`
	SeriousMin      flexFloat       `json:"serious_min"`
	SeriousMax      flexFloat       `json:"serious_max"`
	TakeMargin      flexBool        `json:"take_margin"`
	IsMarginWhite   flexBool        `json:"is_margin_white"`
	IsJoin          flexBool        `json:"is_join"`
	IsJoined        flexBool        `json:"is_joined"`
	HackerJoined    flexBool        `json:"hacker_joined_exists"`
	IsApply         flexBool        `json:"is_apply"`
	IsInvited       flexBool        `json:"is_invited"`
	InviteCode      string          `json:"invite_code"`
	InviteStatus    int             `json:"invitation_status"`
	CityName        string          `json:"city_name"`
	BusinessType    flexString      `json:"business_type"`
	HallProjectType string          `json:"hall_project_type_name"`
}

func (p projectItemJSON) effectiveID() int64 {
	if p.ID.Int64() > 0 {
		return p.ID.Int64()
	}
	return p.ProjectID.Int64()
}

func (p projectItemJSON) joined() bool {
	return p.IsJoin.Bool() || p.IsJoined.Bool() || p.HackerJoined.Bool()
}

// ListPrograms implements platform.Platform.
//
// Authenticated mode reads:
//  1. /hacker/project/mine/list (joined, private, applied, and pending invites)
//  2. /hacker/project/projects (public project hall)
//  3. /web/project/enterprise/src (Enterprise SRC directory)
//
// It verifies session validity both before and after listing so an expired
// session never causes private programs to appear removed (Complete stays false).
func (c *Client) ListPrograms(ctx context.Context) (platform.Listing, error) {
	var out platform.Listing
	auth := c.authenticated()
	if auth {
		if err := c.ensureSession(ctx); err != nil {
			return out, err
		}
	}

	byHandle := map[string]platform.Program{}
	order := []string{}
	addOrMerge := func(item projectItemJSON, raw json.RawMessage, fromMine bool) {
		prog, ok := c.toProgram(item, raw, fromMine)
		if !ok {
			return
		}
		if existing, exists := byHandle[prog.Handle]; exists {
			byHandle[prog.Handle] = mergeProgram(existing, prog)
			c.recordProjectMeta(prog.Handle, item)
			return
		}
		byHandle[prog.Handle] = prog
		order = append(order, prog.Handle)
		c.recordProjectMeta(prog.Handle, item)
	}

	buildResult := func() []platform.Program {
		progs := make([]platform.Program, 0, len(order))
		for _, h := range order {
			progs = append(progs, byHandle[h])
		}
		return progs
	}

	var autoAccept []projectMeta

	if auth {
		mine, err := c.fetchProjectPages(ctx, "/hacker/project/mine/list?type=-1&status=0&project_status=-1&page=%d&page_size=50", "mine/list")
		for _, r := range mine {
			var item projectItemJSON
			if json.Unmarshal(r, &item) == nil {
				addOrMerge(item, r, true)
				if c.autoJoin && item.IsInvited.Bool() && !item.joined() {
					if m, ok := c.getMeta(strconv.FormatInt(item.effectiveID(), 10)); ok {
						autoAccept = append(autoAccept, m)
					}
				}
			}
		}
		if err != nil {
			out.Programs = buildResult()
			return out, err
		}

		hall, err := c.fetchProjectPages(ctx, "/hacker/project/projects?type=-1&flag=0&order_by=time&direction=desc&page=%d&page_size=50", "hacker/projects")
		for _, r := range hall {
			var item projectItemJSON
			if json.Unmarshal(r, &item) == nil {
				addOrMerge(item, r, false)
			}
		}
		if err != nil {
			out.Programs = buildResult()
			return out, err
		}
	} else {
		pub, err := c.fetchProjectPages(ctx, "/web/project/projects?type=-1&flag=0&order_by=time&direction=desc&page=%d&page_size=50", "web/projects")
		for _, r := range pub {
			var item projectItemJSON
			if json.Unmarshal(r, &item) == nil {
				addOrMerge(item, r, false)
			}
		}
		if err != nil {
			out.Programs = buildResult()
			return out, err
		}
	}

	srcs, err := c.fetchProjectPages(ctx, "/web/project/enterprise/src?activity=0&order_by=normal&page=%d&page_size=50", "enterprise/src")
	for _, r := range srcs {
		var item projectItemJSON
		if json.Unmarshal(r, &item) == nil {
			item.TaskIsSRC = true
			if item.Type == 0 {
				item.Type = 7
			}
			addOrMerge(item, r, false)
		}
	}
	if err != nil {
		out.Programs = buildResult()
		return out, err
	}

	if auth {
		ok, err := c.sessionValid(ctx)
		if err != nil {
			out.Programs = buildResult()
			return out, fmt.Errorf("vulbox: session check after listing: %w", err)
		}
		if !ok {
			out.Programs = buildResult()
			return out, fmt.Errorf("vulbox: session ended while listing, private programs may be missing: %w", ErrSessionExpired)
		}

		// User-authorized AutoJoin: accept non-margin pending invites immediately
		// so invited private projects unlock on the first poll.
		for _, m := range autoAccept {
			if m.TakeMargin && !m.IsMarginWhite {
				c.log.Info("vulbox: skipping auto-accept of margin-gated invite", "project_id", m.ID)
				continue
			}
			if err := c.postInviteAccept(ctx, m.ID, m.InviteCode); err != nil {
				c.log.Warn("vulbox: auto-accept invite skipped", "project_id", m.ID, "err", err)
				continue
			}
			c.setMeta(strconv.FormatInt(m.ID, 10), func(pm *projectMeta) {
				pm.IsInvited = false
				pm.IsJoined = true
			})
		}
	}

	out.Programs = buildResult()
	out.Complete = true
	return out, nil
}

func (c *Client) fetchProjectPages(ctx context.Context, pathFmt, label string) ([]json.RawMessage, error) {
	var items []json.RawMessage
	for pageNo := 1; ; pageNo++ {
		if pageNo > maxPages {
			return items, fmt.Errorf("vulbox: %s exceeded %d pages", label, maxPages)
		}
		var page pagedProjects
		u := fmt.Sprintf(pathFmt, pageNo)
		if err := c.getJSON(ctx, u, nil, &page); err != nil {
			return items, fmt.Errorf("vulbox: %s page %d: %w", label, pageNo, err)
		}
		items = append(items, page.Data...)
		if len(page.Data) == 0 || page.LastPage <= 0 || pageNo >= page.LastPage {
			break
		}
	}
	return items, nil
}

func (c *Client) recordProjectMeta(handle string, item projectItemJSON) {
	c.setMeta(handle, func(m *projectMeta) {
		if id := item.effectiveID(); id > 0 {
			m.ID = id
		}
		if item.SRCURL != "" {
			m.SRCURL = strings.TrimSpace(item.SRCURL)
		}
		if item.InviteCode != "" {
			m.InviteCode = item.InviteCode
		}
		if item.IsInvited.Bool() {
			m.IsInvited = true
		}
		if item.joined() {
			m.IsJoined = true
		}
		if item.TakeMargin.Bool() {
			m.TakeMargin = true
		}
		if item.IsMarginWhite.Bool() {
			m.IsMarginWhite = true
		}
		if item.TaskIsSRC.Bool() || item.Type == 7 {
			m.IsSRC = true
		}
		if item.Type != 0 {
			m.ProjectType = item.Type
		}
		if item.TaskTitle != "" {
			m.Title = strings.TrimSpace(item.TaskTitle)
		}
	})
}

func (c *Client) toProgram(item projectItemJSON, raw json.RawMessage, fromMine bool) (platform.Program, bool) {
	id := item.effectiveID()
	if id <= 0 {
		return platform.Program{}, false
	}
	handle := strconv.FormatInt(id, 10)
	name := strings.TrimSpace(item.TaskTitle)
	if name == "" {
		name = "VulBox #" + handle
	}

	link := fmt.Sprintf("%s/projectHall/projectDetail/%d", c.user.String(), id)
	if g := strings.TrimSpace(item.Goto); strings.HasPrefix(g, "https://") || strings.HasPrefix(g, "http://") {
		link = g
	} else if item.SRCURL != "" {
		link = "https://" + strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(item.SRCURL), "https://"), "http://")
	} else if w := strings.TrimSpace(item.TaskWebsite); strings.HasPrefix(w, "https://") || strings.HasPrefix(w, "http://") {
		link = w
	}

	private := isPrivateType(item.Type) || isPrivateType(item.ProjectType) || item.IsStaticTask.Bool()
	if fromMine && item.IsInvited.Bool() && !item.TaskIsSRC.Bool() && item.Type != 0 && item.Type != 7 && item.Type != 8 {
		private = true
	}

	stateCode := item.Status
	if item.ProjectStatus != nil {
		stateCode = *item.ProjectStatus
	} else if item.TaskStatus != nil && stateCode == 0 {
		stateCode = *item.TaskStatus
	}

	p := platform.Program{
		Handle:     handle,
		PlatformID: handle,
		Name:       name,
		URL:        link,
		Private:    private,
		State:      mapProgramState(stateCode),
		StartedAt:  parseCSTTime(item.TaskSTime),
		Raw:        raw,
	}

	lo, hi := extractListingBounty(item)
	if hi > 0 {
		p.OffersBounty = true
		p.Currency = currencyCNY
		p.BountyMax = &hi
		if lo > 0 {
			p.BountyMin = &lo
		}
	} else if item.TaskBonusLevel > 0 {
		p.OffersBounty = true
		p.Currency = currencyCNY
	}
	return p, true
}

func mergeProgram(base, add platform.Program) platform.Program {
	if base.Name == "" || strings.HasPrefix(base.Name, "VulBox #") {
		base.Name = add.Name
	}
	if add.Private {
		base.Private = true
	}
	if base.StartedAt == nil && add.StartedAt != nil {
		base.StartedAt = add.StartedAt
	}
	if add.BountyMax != nil && (base.BountyMax == nil || *add.BountyMax > *base.BountyMax) {
		base.OffersBounty = true
		base.Currency = currencyCNY
		base.BountyMax = add.BountyMax
	}
	if add.BountyMin != nil && (base.BountyMin == nil || *add.BountyMin < *base.BountyMin) {
		base.BountyMin = add.BountyMin
	}
	if base.State == platform.StateOpen && add.State != platform.StateOpen {
		base.State = add.State
	}
	return base
}

func isPrivateType(t int) bool {
	switch t {
	case 2, 3, 11, 12:
		return true
	default:
		return false
	}
}

func mapProgramState(code int) string {
	switch code {
	case 0:
		return platform.StateOpen
	case 1, 2:
		return platform.StatePaused
	case 3:
		return platform.StateClosed
	default:
		return platform.StateOpen
	}
}

func extractListingBounty(item projectItemJSON) (lo, hi float64) {
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
	if item.BonusRange != nil {
		br := item.BonusRange
		for _, v := range []flexFloat{br.LowMin, br.LowMax, br.MiddleMin, br.MiddleMax, br.HighMin, br.HighMax, br.SeriousMin, br.SeriousMax} {
			consider(v.Float64())
		}
	}
	for _, v := range []flexFloat{item.LowMin, item.LowMax, item.MiddleMin, item.MiddleMax, item.HighMin, item.HighMax, item.SeriousMin, item.SeriousMax} {
		consider(v.Float64())
	}
	if hi == 0 && item.RewardRange != "" {
		rLo, rHi := parseRewardRange(item.RewardRange)
		consider(rLo)
		consider(rHi)
	}
	return lo, hi
}

// parseRewardRange parses strings like "100 ~ 10000" or "¥500-¥5,000".
func parseRewardRange(s string) (lo, hi float64) {
	cleaned := strings.NewReplacer("¥", " ", "￥", " ", "元", " ", ",", "", "~", " ", "-", " ", "—", " ", "至", " ").Replace(s)
	for _, tok := range strings.Fields(cleaned) {
		v, err := strconv.ParseFloat(tok, 64)
		if err != nil || v <= 0 {
			continue
		}
		if lo == 0 || v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	return lo, hi
}
