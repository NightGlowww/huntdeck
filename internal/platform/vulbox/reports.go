package vulbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// VulBox partitions submitted reports across 5 project-category tabs:
//
//	0  = 常规漏洞 (Normal / Public SRC)
//	9  = 企业SRC漏洞 (Enterprise SRC)
//	3  = 私密项目漏洞 (Private)
//	5  = 闪测漏洞 (Flash Test)
//	11 = 攻防/专属漏洞 (Private Attack/Defense)
var reportTabs = []int{0, 9, 3, 5, 11}

type pagedReports struct {
	CurrentPage int              `json:"current_page"`
	LastPage    int              `json:"last_page"`
	PerPage     int              `json:"per_page"`
	Total       int              `json:"total"`
	Data        []reportItemJSON `json:"data"`
}

// reportItemJSON captures metadata-only fields from /hacker/vulnerability/list.
// HuntDeck NEVER fetches write-up bodies (/hacker/bugs/detail/base) or comments.
type reportItemJSON struct {
	ID                flexInt64  `json:"id"`
	BugLongID         string     `json:"bug_long_id"`
	BugTitle          string     `json:"bug_title"`
	BugTitleLong      string     `json:"bug_title_long"`
	BugTID            flexInt64  `json:"bug_tid"`
	ProjectID         flexInt64  `json:"project_id"`
	TaskTitle         string     `json:"task_title"`
	ProjectName       string     `json:"project_name"`
	BugFirmName       string     `json:"bug_firm_name"`
	BugType           int        `json:"bug_type"`
	BugLevel          int        `json:"bug_level"`
	BugFinalLevel     int        `json:"bug_final_level"`
	BugLevelR         int        `json:"bug_level_r"`
	BugLevelY         int        `json:"bug_level_y"`
	BugFinalLevelName string     `json:"bug_final_level_name"`
	BugStatus         *int       `json:"bug_status"`
	Status            *int       `json:"status"`
	BugStatusDesc     string     `json:"bug_status_desc"`
	BugValid          int        `json:"bug_valid"`
	Valid             string     `json:"valid"`
	BugQuality        int        `json:"bug_quality"`
	BugMoney          flexFloat  `json:"bug_money"`
	BugAward          flexFloat  `json:"bug_award"`
	BugActivityMoney  flexFloat  `json:"bug_activity_money"`
	BugTotalScore     flexInt64  `json:"bug_total_score"`
	BugUpdata         flexString `json:"bug_updata"`
	CreatedAt         flexString `json:"created_at"`
	UpdateAt          flexString `json:"update_at"`
	BugAuditTime      flexInt64  `json:"bug_audit_time"`
	BugApproveTime    flexInt64  `json:"bug_approve_time"`
}

func (r reportItemJSON) effectiveStatus() int {
	if r.BugStatus != nil {
		return *r.BugStatus
	}
	if r.Status != nil {
		return *r.Status
	}
	return 0
}

func (r reportItemJSON) effectiveTID() int64 {
	if r.BugTID.Int64() > 0 {
		return r.BugTID.Int64()
	}
	return r.ProjectID.Int64()
}

type submittedProjectsPage struct {
	CurrentPage int `json:"current_page"`
	LastPage    int `json:"last_page"`
	Data        []struct {
		ID        flexInt64 `json:"id"`
		TaskID    flexInt64 `json:"task_id"`
		BugTID    flexInt64 `json:"bug_tid"`
		TaskTitle string    `json:"task_title"`
		Title     string    `json:"title"`
		Name      string    `json:"name"`
	} `json:"data"`
}

// ReportsConfigured implements platform.ReportFetcher.
func (c *Client) ReportsConfigured() bool { return c.authenticated() }

// FetchReports implements platform.ReportFetcher.
// It iterates all 5 report type tabs, deduplicates by report ID, resolves
// missing Enterprise SRC program titles via /hacker/vulnerability/projects/submitted,
// converts UTC+8 timestamps to UTC, and emits CNY payments. It NEVER fetches
// write-up bodies or comments.
func (c *Client) FetchReports(ctx context.Context, _ map[string]string, _ int, save platform.ReportsSaver) (bool, error) {
	if !c.authenticated() {
		return false, platform.ErrNoWebLogin
	}
	if err := c.ensureSession(ctx); err != nil {
		return false, fmt.Errorf("vulbox: reports: %w", err)
	}

	seen := map[int64]bool{}
	titles := map[int64]string{}
	loadedSubmitted := map[int]bool{}
	var batch []platform.Report

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := save(ctx, batch); err != nil {
			return err
		}
		batch = nil
		return nil
	}

	for _, tab := range reportTabs {
		for pageNo := 1; ; pageNo++ {
			if pageNo > maxPages {
				_ = flush()
				return false, fmt.Errorf("vulbox: reports tab %d exceeded %d pages", tab, maxPages)
			}
			var page pagedReports
			u := fmt.Sprintf("/hacker/vulnerability/list?status=0&type=%d&page=%d&per_page=50", tab, pageNo)
			if err := c.getJSON(ctx, u, nil, &page); err != nil {
				_ = flush()
				return false, fmt.Errorf("vulbox: reports tab %d page %d: %w", tab, pageNo, err)
			}

			// Resolve missing program titles (especially Enterprise SRC tab 9 where
			// bug_firm_name is often empty) using /hacker/vulnerability/projects/submitted.
			if !loadedSubmitted[tab] && needsTitleLookup(page.Data, titles) {
				loadedSubmitted[tab] = true
				c.loadSubmittedProjectTitles(ctx, tab, titles)
			}

			for _, item := range page.Data {
				id := item.ID.Int64()
				if id <= 0 || seen[id] {
					continue
				}
				seen[id] = true
				r := c.convertReport(item, titles)
				if r.Key == "" || r.SubmittedAt.IsZero() {
					continue
				}
				batch = append(batch, r)
				if len(batch) >= 25 {
					if err := flush(); err != nil {
						return false, err
					}
				}
			}

			if len(page.Data) == 0 || page.LastPage <= 0 || pageNo >= page.LastPage {
				break
			}
		}
	}

	if err := flush(); err != nil {
		return false, err
	}
	return true, nil
}

func needsTitleLookup(items []reportItemJSON, titles map[int64]string) bool {
	for _, it := range items {
		tid := it.effectiveTID()
		if tid <= 0 {
			continue
		}
		if strings.TrimSpace(it.TaskTitle) == "" && strings.TrimSpace(it.ProjectName) == "" && titles[tid] == "" {
			return true
		}
	}
	return false
}

func (c *Client) loadSubmittedProjectTitles(ctx context.Context, tab int, into map[int64]string) {
	for pageNo := 1; pageNo <= 20; pageNo++ {
		var p submittedProjectsPage
		u := fmt.Sprintf("/hacker/vulnerability/projects/submitted?page=%d&per_page=100&type=%d", pageNo, tab)
		if err := c.getJSON(ctx, u, nil, &p); err != nil {
			c.log.Debug("vulbox: submitted projects lookup failed", "tab", tab, "page", pageNo, "err", err)
			return
		}
		for _, row := range p.Data {
			id := row.ID.Int64()
			if id <= 0 {
				id = row.TaskID.Int64()
			}
			if id <= 0 {
				id = row.BugTID.Int64()
			}
			title := firstNonEmpty(row.TaskTitle, row.Title, row.Name)
			if id > 0 && title != "" {
				into[id] = title
			}
		}
		if len(p.Data) == 0 || p.LastPage <= 0 || pageNo >= p.LastPage {
			break
		}
	}
}

func (c *Client) convertReport(item reportItemJSON, titles map[int64]string) platform.Report {
	id := item.ID.Int64()
	key := strconv.FormatInt(id, 10)
	tid := item.effectiveTID()

	progHandle := ""
	if tid > 0 {
		progHandle = strconv.FormatInt(tid, 10)
	}
	progName := firstNonEmpty(item.TaskTitle, item.ProjectName, titles[tid], item.BugFirmName)
	if progName == "" && progHandle != "" {
		if m, ok := c.getMeta(progHandle); ok && m.Title != "" {
			progName = m.Title
		}
	}

	status := item.effectiveStatus()
	state, waitingOn := mapReportState(status, item.BugValid)
	rawState := strings.TrimSpace(item.BugStatusDesc)
	if v := strings.TrimSpace(item.Valid); v != "" {
		if rawState != "" && rawState != v {
			rawState += " (" + v + ")"
		} else if rawState == "" {
			rawState = v
		}
	}

	sev := mapReportSeverity(item)
	sevLabel := strings.TrimSpace(item.BugFinalLevelName)

	submittedAt := parseCSTString(item.BugUpdata.String())
	if submittedAt == nil {
		submittedAt = parseCSTString(item.CreatedAt.String())
	}
	updatedAt := parseCSTString(item.UpdateAt.String())

	var triagedAt, approvedAt *time.Time
	if ts := item.BugAuditTime.Int64(); ts > 0 {
		t := time.Unix(ts, 0).UTC()
		triagedAt = &t
	}
	if ts := item.BugApproveTime.Int64(); ts > 0 {
		t := time.Unix(ts, 0).UTC()
		approvedAt = &t
	}

	bounty := item.BugMoney.Float64()
	if bounty <= 0 {
		bounty = item.BugAward.Float64()
	}
	bonus := item.BugActivityMoney.Float64()

	var payments []platform.ReportPayment
	var firstBountyAt *time.Time
	awardedAt := time.Time{}
	switch {
	case approvedAt != nil:
		awardedAt = *approvedAt
	case updatedAt != nil:
		awardedAt = *updatedAt
	case submittedAt != nil:
		awardedAt = *submittedAt
	}
	if bounty > 0 {
		payments = append(payments, platform.ReportPayment{
			Ref:       key + "-bounty",
			Kind:      "bounty",
			Amount:    bounty,
			Currency:  currencyCNY,
			AwardedAt: awardedAt,
		})
	}
	if bonus > 0 {
		payments = append(payments, platform.ReportPayment{
			Ref:       key + "-bonus",
			Kind:      "bonus",
			Amount:    bonus,
			Currency:  currencyCNY,
			AwardedAt: awardedAt,
		})
	}
	if len(payments) > 0 && !awardedAt.IsZero() {
		t := awardedAt
		firstBountyAt = &t
	}

	var closedAt *time.Time
	if isTerminalReportState(state) {
		switch {
		case updatedAt != nil:
			closedAt = updatedAt
		case approvedAt != nil:
			closedAt = approvedAt
		case triagedAt != nil:
			closedAt = triagedAt
		}
	}

	r := platform.Report{
		Key:            key,
		ProgramHandle:  progHandle,
		ProgramName:    progName,
		Title:          firstNonEmpty(item.BugTitle, item.BugTitleLong),
		URL:            fmt.Sprintf("%s/management/myBugs/detail/%d", c.user.String(), id),
		State:          state,
		RawState:       rawState,
		Severity:       sev,
		SeverityLabel:  sevLabel,
		TriagedAt:      triagedAt,
		FirstBountyAt:  firstBountyAt,
		ClosedAt:       closedAt,
		LastActivityAt: updatedAt,
		WaitingOn:      waitingOn,
		Detailed:       true,
		PaymentsKnown:  true,
		Payments:       payments,
		Marker:         fmt.Sprintf("%d|%d|%s|%.2f|%.2f|%s", status, item.BugValid, sev, bounty, bonus, item.UpdateAt.String()),
	}
	if submittedAt != nil {
		r.SubmittedAt = *submittedAt
	}

	// Allow-list metadata only (never write-ups or comments).
	r.Raw, _ = json.Marshal(map[string]any{
		"id":                 id,
		"bug_long_id":        item.BugLongID,
		"bug_tid":            tid,
		"bug_type":           item.BugType,
		"bug_status":         status,
		"bug_valid":          item.BugValid,
		"bug_level":          item.BugLevel,
		"bug_quality":        item.BugQuality,
		"bug_money":          bounty,
		"bug_activity_money": bonus,
		"bug_total_score":    item.BugTotalScore.Int64(),
	})
	return r
}

func mapReportState(status, valid int) (state, waitingOn string) {
	switch status {
	case 1: // 待审阅
		return platform.ReportNew, "program"
	case 2: // 待确认
		return platform.ReportTriaged, "program"
	case 7: // 待补充
		return platform.ReportTriaged, "you"
	case 3: // 待关闭 (confirmed valid, pending close)
		if st, ok := mapIgnoredValidity(valid); ok {
			return st, ""
		}
		return platform.ReportAccepted, "program"
	case 4: // 已关闭
		if st, ok := mapIgnoredValidity(valid); ok {
			return st, ""
		}
		return platform.ReportResolved, ""
	case 5: // 已忽略
		if st, ok := mapIgnoredValidity(valid); ok {
			return st, ""
		}
		return platform.ReportNotApplicable, ""
	default:
		if st, ok := mapIgnoredValidity(valid); ok {
			return st, ""
		}
		return platform.ReportNew, ""
	}
}

func mapIgnoredValidity(valid int) (string, bool) {
	switch valid {
	case 2: // 重复漏洞
		return platform.ReportDuplicate, true
	case 3: // 超出范围
		return platform.ReportOutOfScope, true
	case 4: // 其他
		return platform.ReportInformative, true
	case 5: // 无法复现
		return platform.ReportNotApplicable, true
	default:
		return "", false
	}
}

func isTerminalReportState(state string) bool {
	switch state {
	case platform.ReportResolved, platform.ReportDuplicate, platform.ReportOutOfScope,
		platform.ReportInformative, platform.ReportNotApplicable, platform.ReportSpam:
		return true
	default:
		return false
	}
}

func mapReportSeverity(item reportItemJSON) string {
	for _, code := range []int{item.BugFinalLevel, item.BugLevel, item.BugLevelR, item.BugLevelY} {
		switch code {
		case 1:
			return platform.SeverityLow
		case 2:
			return platform.SeverityMedium
		case 3:
			return platform.SeverityHigh
		case 4:
			return platform.SeverityCritical
		}
	}
	return ""
}
