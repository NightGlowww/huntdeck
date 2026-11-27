package issuehunt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

const (
	reportsPageSize    = 200
	activitiesPageSize = 100
)

type pagedReports struct {
	Data    []reportJSON `json:"data"`
	Page    int          `json:"page"`
	PerPage int          `json:"perPage"`
	Total   int          `json:"total"`
}

type reportJSON struct {
	ID       string `json:"id"`
	Seen     bool   `json:"seen"`
	Metadata struct {
		Title  string `json:"title"`
		Number int64  `json:"number"`
		State  string `json:"state"`
		SentAt string `json:"sentAt"`
		Scope  *struct {
			Type        []string `json:"type"`
			Identifier  string   `json:"identifier"`
			VRTID       string   `json:"vrtId"`
			Criticality string   `json:"criticality"`
		} `json:"scope"`
	} `json:"metadata"`
	Program *struct {
		ID           string  `json:"id"`
		Slug         string  `json:"slug"`
		Name         string  `json:"name"`
		Type         string  `json:"type"`
		Activated    bool    `json:"activated"`
		CVSSVersion  string  `json:"cvssVersion"`
		CreditSystem *string `json:"creditSystem"`
		Organization *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"organization"`
	} `json:"program"`
	Messages *struct {
		Count int `json:"count"`
	} `json:"messages"`
	Assessment    *assessmentJSON `json:"assessment"`
	OrgAssessment *assessmentJSON `json:"orgAssessment"`
	CreatedAt     string          `json:"createdAt"`
}

type assessmentJSON struct {
	Type     string   `json:"type"`
	Severity string   `json:"severity"`
	Score    *float64 `json:"score"`
}

type pagedActivities struct {
	Data    []activityJSON `json:"data"`
	Page    int            `json:"page"`
	PerPage int            `json:"perPage"`
	Total   int            `json:"total"`
}

type activityJSON struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	CreatedAt string          `json:"createdAt"`
	Date      string          `json:"date"`
	Report    json.RawMessage `json:"report"`
	ReportID  string          `json:"reportId"`
	Info      *struct {
		Amount   float64 `json:"amount"`
		System   string  `json:"system"`
		Currency string  `json:"currency"`
		Report   string  `json:"report"`
		ReportID string  `json:"reportId"`
	} `json:"info"`
}

// ReportsConfigured implements platform.ReportFetcher.
func (c *Client) ReportsConfigured() bool {
	return c.authenticated()
}

// FetchReports implements platform.ReportFetcher.
// It fetches GET /reports?perPage=200&page=1..N and GET /users/{userID}/activities
// to populate Paid bounties from "report.rewarded" events (info.system != "point").
// It NEVER calls /messages.
func (c *Client) FetchReports(ctx context.Context, _ map[string]string, _ int, save platform.ReportsSaver) (bool, error) {
	if !c.authenticated() {
		return false, ErrAuth
	}
	userID, err := c.ensureAuth(ctx)
	if err != nil {
		return false, fmt.Errorf("issuehunt: reports auth: %w", err)
	}

	paymentsByReport, lastActByReport, activitiesOK, err := c.fetchRewardActivities(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("issuehunt: reports activities: %w", err)
	}

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

	for pageNo := 1; ; pageNo++ {
		if pageNo > maxPages {
			_ = flush()
			return false, fmt.Errorf("issuehunt: reports exceeded %d pages", maxPages)
		}
		var page pagedReports
		path := fmt.Sprintf("/reports?perPage=%d&page=%d", reportsPageSize, pageNo)
		if err := c.getJSON(ctx, path, &page); err != nil {
			_ = flush()
			return false, fmt.Errorf("issuehunt: reports page %d: %w", pageNo, err)
		}

		for _, item := range page.Data {
			if strings.TrimSpace(item.ID) == "" {
				continue
			}
			payments := paymentsByReport[item.ID]
			actAt := lastActByReport[item.ID]
			itemPaymentsKnown := activitiesOK
			if !activitiesOK {
				if rp, ra, ok, rerr := c.fetchSingleReportActivities(ctx, item.ID); rerr != nil {
					_ = flush()
					return false, fmt.Errorf("issuehunt: report %s activities: %w", item.ID, rerr)
				} else if ok {
					payments = rp
					actAt = ra
					itemPaymentsKnown = true
				}
			}
			r := c.convertReport(item, payments, actAt, itemPaymentsKnown)
			batch = append(batch, r)
			if len(batch) >= 50 {
				if err := flush(); err != nil {
					return false, err
				}
			}
		}

		if len(page.Data) == 0 {
			break
		}
		perPage := page.PerPage
		if perPage <= 0 {
			perPage = reportsPageSize
		}
		if page.Total > 0 {
			if (pageNo-1)*perPage+len(page.Data) >= page.Total {
				break
			}
		} else if len(page.Data) < perPage {
			break
		}
	}

	if err := flush(); err != nil {
		return false, err
	}
	return true, nil
}

func (c *Client) fetchRewardActivities(ctx context.Context, userID string) (
	payments map[string][]platform.ReportPayment,
	lastAt map[string]time.Time,
	ok bool,
	err error,
) {
	payments = map[string][]platform.ReportPayment{}
	lastAt = map[string]time.Time{}

	for pageNo := 1; ; pageNo++ {
		if pageNo > maxPages {
			return payments, lastAt, false, fmt.Errorf("activities exceeded %d pages", maxPages)
		}
		var page pagedActivities
		path := fmt.Sprintf("/users/%s/activities?page=%d&perPage=%d", url.PathEscape(userID), pageNo, activitiesPageSize)
		if err := c.getJSON(ctx, path, &page); err != nil {
			// If user activities endpoint is unavailable on this deployment, return
			// PaymentsKnown=false without failing the whole report sync unless auth failed.
			if isHTTPStatus(err, 404) {
				return payments, lastAt, false, nil
			}
			return payments, lastAt, false, err
		}

		for idx, act := range page.Data {
			rid := extractActivityReportID(act)
			if rid == "" {
				continue
			}
			at := parseTime(firstNonEmpty(act.CreatedAt, act.Date))
			if at != nil {
				if cur, exists := lastAt[rid]; !exists || at.After(cur) {
					lastAt[rid] = *at
				}
			}
			if act.Type != "report.rewarded" || act.Info == nil {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(act.Info.System), "point") || act.Info.Amount <= 0 {
				continue
			}
			cur := currencyJPY
			if cstr := strings.ToUpper(strings.TrimSpace(act.Info.Currency)); cstr != "" {
				cur = cstr
			}
			ref := strings.TrimSpace(act.ID)
			if ref == "" {
				ref = fmt.Sprintf("%s-reward-%d-%d", rid, pageNo, idx)
			}
			var awardedAt time.Time
			if at != nil {
				awardedAt = *at
			}
			payments[rid] = append(payments[rid], platform.ReportPayment{
				Ref:       ref,
				Kind:      "bounty",
				Amount:    act.Info.Amount,
				Currency:  cur,
				AwardedAt: awardedAt,
			})
		}

		if len(page.Data) == 0 {
			break
		}
		perPage := page.PerPage
		if perPage <= 0 {
			perPage = activitiesPageSize
		}
		if page.Total > 0 {
			if (pageNo-1)*perPage+len(page.Data) >= page.Total {
				break
			}
		} else if len(page.Data) < perPage {
			break
		}
	}

	return payments, lastAt, true, nil
}

func (c *Client) fetchSingleReportActivities(ctx context.Context, reportID string) (
	payments []platform.ReportPayment,
	lastAt time.Time,
	ok bool,
	err error,
) {
	path := fmt.Sprintf("/reports/%s/activities?page=1&perPage=%d", url.PathEscape(reportID), activitiesPageSize)
	var page pagedActivities
	if err := c.getJSON(ctx, path, &page); err != nil {
		if isHTTPStatus(err, 404, 403) {
			return nil, time.Time{}, false, nil
		}
		return nil, time.Time{}, false, err
	}
	for idx, act := range page.Data {
		at := parseTime(firstNonEmpty(act.CreatedAt, act.Date))
		if at != nil && at.After(lastAt) {
			lastAt = *at
		}
		if act.Type != "report.rewarded" || act.Info == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(act.Info.System), "point") || act.Info.Amount <= 0 {
			continue
		}
		cur := currencyJPY
		if cstr := strings.ToUpper(strings.TrimSpace(act.Info.Currency)); cstr != "" {
			cur = cstr
		}
		ref := strings.TrimSpace(act.ID)
		if ref == "" {
			ref = fmt.Sprintf("%s-reward-%d", reportID, idx)
		}
		var awardedAt time.Time
		if at != nil {
			awardedAt = *at
		}
		payments = append(payments, platform.ReportPayment{
			Ref:       ref,
			Kind:      "bounty",
			Amount:    act.Info.Amount,
			Currency:  cur,
			AwardedAt: awardedAt,
		})
	}
	return payments, lastAt, true, nil
}

func extractActivityReportID(act activityJSON) string {
	if id := strings.TrimSpace(act.ReportID); id != "" {
		return id
	}
	if len(act.Report) > 0 {
		var s string
		if json.Unmarshal(act.Report, &s) == nil && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
		var obj struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(act.Report, &obj) == nil && strings.TrimSpace(obj.ID) != "" {
			return strings.TrimSpace(obj.ID)
		}
	}
	if act.Info != nil {
		return firstNonEmpty(act.Info.ReportID, act.Info.Report)
	}
	return ""
}

func (c *Client) convertReport(item reportJSON, payments []platform.ReportPayment, actAt time.Time, paymentsKnown bool) platform.Report {
	var progHandle, progName string
	var isPointProg bool
	if item.Program != nil {
		progHandle = firstNonEmpty(item.Program.Slug, item.Program.ID)
		progName = strings.TrimSpace(item.Program.Name)
		isPointProg = item.Program.CreditSystem != nil && strings.EqualFold(strings.TrimSpace(*item.Program.CreditSystem), "point")
	}

	rawState := strings.TrimSpace(item.Metadata.State)
	state := mapReportState(rawState)

	var submittedSev string
	if item.Metadata.Scope != nil {
		submittedSev = mapSeverity(item.Metadata.Scope.Criticality)
	}
	sev, cvss := effectiveAssessment(item, submittedSev)
	sevChanged := submittedSev != "" && sev != "" && submittedSev != sev

	submittedAt := parseTime(firstNonEmpty(item.Metadata.SentAt, item.CreatedAt))

	var comments int
	if item.Messages != nil {
		comments = item.Messages.Count
	}

	var filteredPayments []platform.ReportPayment
	var totalPaid float64
	var firstBountyAt *time.Time
	if !isPointProg {
		for _, p := range payments {
			if p.AwardedAt.IsZero() && submittedAt != nil {
				p.AwardedAt = *submittedAt
			}
			filteredPayments = append(filteredPayments, p)
			totalPaid += p.Amount
			if !p.AwardedAt.IsZero() && (firstBountyAt == nil || p.AwardedAt.Before(*firstBountyAt)) {
				t := p.AwardedAt
				firstBountyAt = &t
			}
		}
	}

	var lastActivity *time.Time
	if !actAt.IsZero() {
		t := actAt
		lastActivity = &t
	} else if submittedAt != nil {
		t := *submittedAt
		lastActivity = &t
	}

	var closedAt *time.Time
	if isTerminalReportState(state) && lastActivity != nil {
		t := *lastActivity
		closedAt = &t
	}

	r := platform.Report{
		Key:               item.ID,
		ProgramHandle:     progHandle,
		ProgramName:       progName,
		Title:             strings.TrimSpace(item.Metadata.Title),
		URL:               strings.TrimSuffix(c.web.String(), "/") + "/reports/" + url.PathEscape(item.ID),
		State:             state,
		RawState:          rawState,
		Severity:          sev,
		SubmittedSeverity: submittedSev,
		SeverityChanged:   sevChanged,
		CVSS:              cvss,
		FirstBountyAt:     firstBountyAt,
		ClosedAt:          closedAt,
		LastActivityAt:    lastActivity,
		Comments:          comments,
		Unread:            !item.Seen,
		Detailed:          true,
		PaymentsKnown:     paymentsKnown,
		Payments:          filteredPayments,
		Marker:            fmt.Sprintf("%s|%s|%d|%.2f|%d|%v", rawState, sev, comments, totalPaid, len(filteredPayments), item.Seen),
	}
	if submittedAt != nil {
		r.SubmittedAt = *submittedAt
	}

	// Allow-list metadata only (never write-up text or messages).
	r.Raw, _ = json.Marshal(map[string]any{
		"id":             item.ID,
		"number":         item.Metadata.Number,
		"state":          rawState,
		"sentAt":         item.Metadata.SentAt,
		"program_slug":   progHandle,
		"messages_count": comments,
		"assessment":     item.Assessment,
		"orgAssessment":  item.OrgAssessment,
	})
	return r
}

func effectiveAssessment(item reportJSON, fallbackSev string) (sev string, cvss *float64) {
	for _, a := range []*assessmentJSON{item.OrgAssessment, item.Assessment} {
		if a == nil {
			continue
		}
		if cvss == nil && a.Score != nil && *a.Score > 0 {
			s := *a.Score
			cvss = &s
		}
		if sev == "" {
			sev = mapSeverity(a.Severity)
		}
	}
	if sev == "" {
		sev = fallbackSev
	}
	return sev, cvss
}

func mapReportState(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "new", "checked":
		return platform.ReportNew
	case "unresolved", "triaged":
		return platform.ReportTriaged
	case "accepted":
		return platform.ReportAccepted
	case "resolved":
		return platform.ReportResolved
	case "accepted_risk":
		return platform.ReportAcceptedRisk
	case "informational":
		return platform.ReportInformative
	case "duplicate":
		return platform.ReportDuplicate
	case "out_of_scope":
		return platform.ReportOutOfScope
	case "not_applicable", "not_reproducible", "not_reproducable": //nolint:misspell // IssueHunt API status spelling
		return platform.ReportNotApplicable
	case "spam":
		return platform.ReportSpam
	default:
		return platform.ReportNew
	}
}

func isTerminalReportState(state string) bool {
	switch state {
	case platform.ReportResolved, platform.ReportDuplicate, platform.ReportOutOfScope,
		platform.ReportInformative, platform.ReportNotApplicable, platform.ReportSpam,
		platform.ReportAcceptedRisk:
		return true
	default:
		return false
	}
}

func mapSeverity(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "critical":
		return platform.SeverityCritical
	case "high":
		return platform.SeverityHigh
	case "medium":
		return platform.SeverityMedium
	case "low":
		return platform.SeverityLow
	case "none", "informational":
		return platform.SeverityNone
	default:
		return ""
	}
}
