package vulbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
)

type projectDetailJSON struct {
	ID                flexInt64       `json:"id"`
	TaskTitle         string          `json:"task_title"`
	TaskDesc          string          `json:"task_desc"`
	TaskFirm          string          `json:"task_firm"`
	TaskRange         string          `json:"task_range"`
	TaskRequirement   string          `json:"task_requirement"`
	TaskDescription   string          `json:"task_description"`
	AttackRules       string          `json:"attack_rules"`
	TaskSTime         json.RawMessage `json:"task_stime"`
	TaskStartTime     json.RawMessage `json:"task_start_time"`
	TaskIsSRC         flexBool        `json:"task_is_src"`
	TaskValidation    flexString      `json:"task_validation"`
	Type              int             `json:"type"`
	ProjectType       int             `json:"project_type"`
	Status            int             `json:"status"`
	TaskStatus        *int            `json:"task_status"`
	Archived          flexBool        `json:"archived"`
	AuditApplication  flexBool        `json:"audit_application"`
	AnswerQuestion    flexBool        `json:"answer_question"`
	IsJoined          flexBool        `json:"is_joined"`
	IsApplied         flexBool        `json:"is_applied"`
	IsInvited         flexBool        `json:"is_invited"`
	JoinAvailable     flexBool        `json:"join_available"`
	NeedSignAgreement flexBool        `json:"need_sign_agreement"`
	HideProjectInfo   flexBool        `json:"hideProjectInfo"`
	HallStatus        int             `json:"hall_status"`
	HallStatusName    string          `json:"hall_status_name"`
	TakeMargin        flexBool        `json:"take_margin"`
	IsMarginWhite     flexBool        `json:"is_margin_white"`
	MarginAmount      flexFloat       `json:"margin_amount"`
	RewardNum         []rewardGroup   `json:"reward_num"`
}

type rewardGroup struct {
	Date string          `json:"date"`
	Data []rewardRowJSON `json:"data"`
}

type rewardRowJSON struct {
	GradeName    string    `json:"grade_name"`
	BusinessType int       `json:"business_type"`
	LowMin       flexFloat `json:"low_min"`
	LowMax       flexFloat `json:"low_max"`
	MiddleMin    flexFloat `json:"middle_min"`
	MiddleMax    flexFloat `json:"middle_max"`
	HighMin      flexFloat `json:"high_min"`
	HighMax      flexFloat `json:"high_max"`
	SeriousMin   flexFloat `json:"serious_min"`
	SeriousMax   flexFloat `json:"serious_max"`
}

type joinInfoJSON struct {
	CheckCondition     []string  `json:"check_condition"`
	IsMobile           flexBool  `json:"is_mobile"`
	IsReal             flexBool  `json:"is_real"`
	IsWeChat           flexBool  `json:"is_we_chat"`
	NeedAnswerQuestion flexInt64 `json:"need_answer_question"`
	TakeMargin         flexBool  `json:"take_margin"`
	IsMarginWhite      flexBool  `json:"is_margin_white"`
	MarginAmount       flexFloat `json:"margin_amount"`
}

type assetsListJSON struct {
	List []*assetItemJSON `json:"list"`
}

type assetItemJSON struct {
	// Live capture schema (/hacker/project/detail/assets).
	Type   string `json:"type"`
	Name   string `json:"name"`
	Host   string `json:"host"`
	Remark string `json:"remark"`
	// Frontend bundle schema fallback.
	ProductionType   string `json:"production_type"`
	ProductionTitle  string `json:"production_title"`
	ProductionHost   string `json:"production_host"`
	ProductionRemark string `json:"production_remark"`
}

// FetchScope implements platform.Platform.
func (c *Client) FetchScope(ctx context.Context, p platform.Program) ([]platform.ScopeEntry, error) {
	entries, _, err := c.FetchScopeDetail(ctx, p)
	return entries, err
}

// FetchScopeDetail implements platform.ScopeDetailFetcher.
func (c *Client) FetchScopeDetail(ctx context.Context, p platform.Program) ([]platform.ScopeEntry, platform.ScopeDetail, error) {
	projectID, err := parseProjectID(p.Handle, p.PlatformID)
	if err != nil {
		return nil, platform.ScopeDetail{}, err
	}
	if !c.authenticated() {
		return nil, platform.ScopeDetail{}, fmt.Errorf("vulbox: project %d requires login to read scope: %w", projectID, platform.ErrScopeUnavailable)
	}
	if err := c.ensureSession(ctx); err != nil {
		return nil, platform.ScopeDetail{}, err
	}

	detail, err := c.fetchProjectDetail(ctx, projectID)
	if err != nil {
		return nil, platform.ScopeDetail{}, err
	}

	meta, _ := c.getMeta(p.Handle)
	detail, joinInfo, err := c.maybeAutoJoin(ctx, projectID, detail, meta)
	if err != nil {
		return nil, buildScopeDetail(detail, joinInfo), err
	}

	var entries []platform.ScopeEntry
	seen := map[string]bool{}
	addEntry := func(e platform.ScopeEntry) {
		key := fmt.Sprintf("%t|%s|%s", e.InScope, e.AssetType, strings.ToLower(strings.TrimSpace(e.Identifier)))
		if e.Identifier == "" || seen[key] {
			return
		}
		seen[key] = true
		entries = append(entries, e)
	}

	if detail.IsJoined.Bool() || detail.HallStatus == 2 {
		assets, aerr := c.fetchStructuredAssets(ctx, projectID)
		if aerr != nil {
			if errors.Is(aerr, ErrSessionExpired) || errors.Is(aerr, context.Canceled) || errors.Is(aerr, context.DeadlineExceeded) {
				return nil, platform.ScopeDetail{}, aerr
			}
			c.log.Debug("vulbox: detail/assets unavailable, falling back to task_range", "project_id", projectID, "err", aerr)
		}
		for _, a := range assets {
			for _, e := range convertAssetItem(a, hasBounty(detail)) {
				addEntry(e)
			}
		}
	}

	for _, e := range parseHTMLScope(detail.TaskRange, hasBounty(detail)) {
		addEntry(e)
	}

	return entries, buildScopeDetail(detail, joinInfo), nil
}

func (c *Client) fetchProjectDetail(ctx context.Context, projectID int64) (projectDetailJSON, error) {
	var detail projectDetailJSON
	u := fmt.Sprintf("/hacker/project/detail?id=%d", projectID)
	if err := c.getJSON(ctx, u, nil, &detail); err != nil {
		return projectDetailJSON{}, fmt.Errorf("vulbox: project %d detail: %w", projectID, err)
	}
	return detail, nil
}

func (c *Client) fetchStructuredAssets(ctx context.Context, projectID int64) ([]*assetItemJSON, error) {
	var res assetsListJSON
	u := fmt.Sprintf("/hacker/project/detail/assets?project_id=%d", projectID)
	if err := c.getJSON(ctx, u, nil, &res); err != nil {
		return nil, err
	}
	out := make([]*assetItemJSON, 0, len(res.List))
	for _, item := range res.List {
		if item != nil {
			out = append(out, item)
		}
	}
	return out, nil
}

// maybeAutoJoin unlocks scope for unjoined / invited / agreement-gated projects
// when AutoJoin is enabled, while strictly refusing margin-deposit projects and
// never spending tickets.
func (c *Client) maybeAutoJoin(ctx context.Context, projectID int64, detail projectDetailJSON, meta projectMeta) (projectDetailJSON, *joinInfoJSON, error) {
	isInvited := detail.IsInvited.Bool() || detail.HallStatus == 8 || strings.TrimSpace(detail.HallStatusName) == "接受邀请"
	needsJoin := !detail.IsJoined.Bool() || detail.HideProjectInfo.Bool() || detail.HallStatus == 1 || strings.TrimSpace(detail.HallStatusName) == "立即加入"
	needsAgreement := detail.NeedSignAgreement.Bool()

	if !isInvited && !needsJoin && !needsAgreement {
		return detail, nil, nil
	}

	if !c.autoJoin {
		if detail.HideProjectInfo.Bool() || !detail.IsJoined.Bool() || needsAgreement {
			return detail, nil, fmt.Errorf("vulbox: project %d requires joining or agreement (VB_AUTO_JOIN=false): %w", projectID, platform.ErrScopeUnavailable)
		}
		return detail, nil, nil
	}

	marginGated := (detail.TakeMargin.Bool() || detail.MarginAmount.Float64() > 0 || meta.TakeMargin) &&
		!(detail.IsMarginWhite.Bool() || meta.IsMarginWhite)
	if marginGated {
		c.log.Info("vulbox: skipping auto-join for margin-gated project", "project_id", projectID)
		return detail, nil, fmt.Errorf("vulbox: project %d requires a cash margin deposit: %w", projectID, platform.ErrScopeUnavailable)
	}

	if strings.TrimSpace(detail.HallStatusName) == "立即报名" || detail.AuditApplication.Bool() {
		return detail, nil, fmt.Errorf("vulbox: project %d requires manual application: %w", projectID, platform.ErrScopeUnavailable)
	}

	var info *joinInfoJSON
	changed := false

	// 1. Accept pending invitation first.
	if isInvited {
		if err := c.postInviteAccept(ctx, projectID, meta.InviteCode); err != nil {
			if isSoftJoinRejection(err) {
				return detail, nil, fmt.Errorf("vulbox: project %d accept invite skipped (%w): %w", projectID, err, platform.ErrScopeUnavailable)
			}
			return detail, nil, fmt.Errorf("vulbox: project %d accept invite: %w", projectID, err)
		}
		changed = true
		var err error
		if detail, err = c.fetchProjectDetail(ctx, projectID); err != nil {
			return detail, nil, err
		}
	}

	// 2. Join eligible unjoined project.
	if !detail.IsJoined.Bool() || detail.HideProjectInfo.Bool() {
		if !detail.TaskIsSRC.Bool() {
			var ji joinInfoJSON
			if err := c.getJSON(ctx, fmt.Sprintf("/hacker/project/join/info?id=%d", projectID), nil, &ji); err == nil {
				info = &ji
				if (ji.TakeMargin.Bool() || ji.MarginAmount.Float64() > 0) && !ji.IsMarginWhite.Bool() {
					return detail, info, fmt.Errorf("vulbox: project %d requires a cash margin deposit: %w", projectID, platform.ErrScopeUnavailable)
				}
				if slices.Contains(ji.CheckCondition, "question") && ji.NeedAnswerQuestion.Int64() == 0 {
					return detail, info, fmt.Errorf("vulbox: project %d requires answering a quiz first: %w", projectID, platform.ErrScopeUnavailable)
				}
			}
		}
		if err := c.postProjectJoin(ctx, projectID); err != nil {
			if isSoftJoinRejection(err) {
				return detail, info, fmt.Errorf("vulbox: project %d auto-join skipped (%w): %w", projectID, err, platform.ErrScopeUnavailable)
			}
			return detail, info, fmt.Errorf("vulbox: project %d auto-join: %w", projectID, err)
		}
		changed = true
		var err error
		if detail, err = c.fetchProjectDetail(ctx, projectID); err != nil {
			return detail, info, err
		}
	}

	// 3. Sign participation/confidentiality agreement if required.
	if detail.NeedSignAgreement.Bool() {
		if err := c.postSignAgreement(ctx, projectID); err != nil {
			if isSoftJoinRejection(err) {
				return detail, info, fmt.Errorf("vulbox: project %d sign agreement skipped (%w): %w", projectID, err, platform.ErrScopeUnavailable)
			}
			return detail, info, fmt.Errorf("vulbox: project %d sign agreement: %w", projectID, err)
		}
		changed = true
		var err error
		if detail, err = c.fetchProjectDetail(ctx, projectID); err != nil {
			return detail, info, err
		}
	}

	if changed {
		c.setMeta(strconv.FormatInt(projectID, 10), func(pm *projectMeta) {
			pm.IsJoined = detail.IsJoined.Bool()
			pm.IsInvited = detail.IsInvited.Bool()
		})
	}

	if detail.HideProjectInfo.Bool() {
		return detail, info, fmt.Errorf("vulbox: project %d scope still hidden after join: %w", projectID, platform.ErrScopeUnavailable)
	}
	return detail, info, nil
}

func (c *Client) postProjectJoin(ctx context.Context, projectID int64) error {
	body := struct {
		ProjectID int64 `json:"project_id"`
		UseTicket int   `json:"use_ticket"`
	}{
		ProjectID: projectID,
		UseTicket: 0, // NEVER spend paid/scarce entry tickets automatically.
	}
	return c.postJSON(ctx, "/hacker/project/join", body, nil, nil)
}

func (c *Client) postSignAgreement(ctx context.Context, projectID int64) error {
	body := struct {
		ProjectID int64 `json:"project_id"`
	}{ProjectID: projectID}
	return c.postJSON(ctx, "/hacker/project/mine/sign-agreement", body, nil, nil)
}

func isSoftJoinRejection(err error) bool {
	return isAPICode(err, 400, 403, 551, 400001)
}

func parseProjectID(handle, platformID string) (int64, error) {
	raw := firstNonEmpty(handle, platformID)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("vulbox: invalid project handle %q", raw)
	}
	return id, nil
}

func hasBounty(d projectDetailJSON) bool {
	for _, g := range d.RewardNum {
		for _, r := range g.Data {
			if r.LowMax > 0 || r.MiddleMax > 0 || r.HighMax > 0 || r.SeriousMax > 0 {
				return true
			}
		}
	}
	return false
}

func convertAssetItem(a *assetItemJSON, bounty bool) []platform.ScopeEntry {
	host := strings.TrimSpace(firstNonEmpty(a.Host, a.ProductionHost))
	cat := strings.TrimSpace(firstNonEmpty(a.Type, a.ProductionType))
	title := strings.TrimSpace(firstNonEmpty(a.Name, a.ProductionTitle))
	remark := strings.TrimSpace(firstNonEmpty(a.Remark, a.ProductionRemark))
	if host == "" && title == "" {
		return nil
	}
	ident := host
	if ident == "" {
		ident = title
	}
	instr := remark
	if title != "" && title != ident {
		if instr != "" {
			instr = title + " — " + instr
		} else {
			instr = title
		}
	}
	return []platform.ScopeEntry{{
		InScope:           true,
		AssetType:         normalizeAssetType(cat, ident),
		PlatformAssetType: cat,
		Identifier:        ident,
		BountyEligible:    bounty,
		Instruction:       instr,
	}}
}

func buildScopeDetail(d projectDetailJSON, ji *joinInfoJSON) platform.ScopeDetail {
	var parts []string
	for _, sec := range []struct {
		heading string
		html    string
	}{
		{"", d.TaskFirm},
		{"## Scope", d.TaskRange},
		{"## Requirements", d.TaskRequirement},
		{"## Rules & Severity", d.TaskDescription},
		{"## Attack Rules", d.AttackRules},
	} {
		if md := htmlToMarkdown(sec.html); md != "" {
			if sec.heading != "" {
				parts = append(parts, sec.heading+"\n\n"+md)
			} else {
				parts = append(parts, md)
			}
		}
	}

	rewards := make([]platform.RewardRow, 0)
	for _, g := range d.RewardNum {
		for _, r := range g.Data {
			row := platform.RewardRow{
				Scope:    rewardScopeName(r),
				Low:      makeRewardRange(r.LowMin.Float64(), r.LowMax.Float64()),
				Medium:   makeRewardRange(r.MiddleMin.Float64(), r.MiddleMax.Float64()),
				High:     makeRewardRange(r.HighMin.Float64(), r.HighMax.Float64()),
				Critical: makeRewardRange(r.SeriousMin.Float64(), r.SeriousMax.Float64()),
			}
			if row.Low.Max > 0 || row.Medium.Max > 0 || row.High.Max > 0 || row.Critical.Max > 0 {
				rewards = append(rewards, row)
			}
		}
	}

	stateCode := d.Status
	if d.TaskStatus != nil && stateCode == 0 {
		stateCode = *d.TaskStatus
	}
	state := mapProgramState(stateCode)
	if d.Archived.Bool() || d.HallStatus == 4 {
		state = platform.StateClosed
	}

	req := &platform.Requirements{
		Application: d.AuditApplication.Bool() || strings.TrimSpace(d.HallStatusName) == "立即报名",
		Terms:       d.NeedSignAgreement.Bool(),
	}
	if ji != nil {
		if slices.Contains(ji.CheckCondition, "real") {
			req.IDVerified = true
		}
		if slices.Contains(ji.CheckCondition, "contract") {
			req.Terms = true
		}
	}
	if strings.Contains(d.TaskValidation.String(), "3") {
		req.IDVerified = true
	}

	var facts *platform.Facts
	started := parseCSTTime(d.TaskSTime)
	if started == nil {
		started = parseCSTTime(d.TaskStartTime)
	}
	if started != nil || len(rewards) > 0 {
		f := platform.Facts{StartedAt: started}
		if len(rewards) > 0 {
			yes := true
			f.OffersBounty = &yes
		}
		facts = &f
	}

	return platform.ScopeDetail{
		Policy:       strings.Join(parts, "\n\n"),
		Rewards:      rewards,
		Requirements: req,
		Facts:        facts,
		State:        state,
	}
}

func rewardScopeName(r rewardRowJSON) string {
	if name := strings.TrimSpace(r.GradeName); name != "" {
		return name
	}
	switch r.BusinessType {
	case 3:
		return "P0"
	case 2:
		return "P1"
	case 1:
		return "P2"
	default:
		return "Default"
	}
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
