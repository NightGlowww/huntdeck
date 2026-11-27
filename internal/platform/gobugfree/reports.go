package gobugfree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

type reportListJSON struct {
	Items []reportItemJSON `json:"items"`
}

// reportItemJSON captures metadata-only fields from GET /reports.
// HuntDeck NEVER fetches report write-ups or comment bodies.
type reportItemJSON struct {
	ID                string   `json:"id"`
	Title             string   `json:"title"`
	OrgSlug           string   `json:"orgSlug"`
	OrgName           string   `json:"orgName"`
	EngagementSlug    string   `json:"engagementSlug"`
	EngagementTitle   string   `json:"engagementTitle"`
	Status            string   `json:"status"`
	Severity          string   `json:"severity"`
	SeveritySubmitted string   `json:"severitySubmitted"`
	CVSSScore         *float64 `json:"cvssScore"`
	SubmittedAt       string   `json:"submittedAt"`
	UpdatedAt         string   `json:"updatedAt"`
}

type payoutItemJSON struct {
	ID          json.RawMessage `json:"id"`
	ReportID    string          `json:"reportId"`
	ReportIDAlt string          `json:"report_id"`
	ReportTitle string          `json:"reportTitle"`
	Title       string          `json:"title"`
	Amount      float64         `json:"amount"`
	Currency    string          `json:"currency"`
	Kind        string          `json:"kind"`
	Type        string          `json:"type"`
	Status      string          `json:"status"`
	AwardedAt   string          `json:"awardedAt"`
	PaidAt      string          `json:"paidAt"`
	CreatedAt   string          `json:"createdAt"`
}

// ReportsConfigured implements platform.ReportFetcher.
func (c *Client) ReportsConfigured() bool {
	return c.authenticated()
}

// FetchReports implements platform.ReportFetcher.
// It fetches report metadata from GET /reports?size=100&page=1..N and payout
// metadata from GET /payouts (if exposed), never fetching write-ups or comments.
func (c *Client) FetchReports(ctx context.Context, _ map[string]string, _ int, save platform.ReportsSaver) (bool, error) {
	if !c.authenticated() {
		return false, platform.ErrNoWebLogin
	}
	if err := c.ensureSession(ctx); err != nil {
		return false, fmt.Errorf("gobugfree: reports: %w", err)
	}

	byReportID, byTitle, payoutsKnown, err := c.fetchPayouts(ctx)
	if err != nil {
		return false, fmt.Errorf("gobugfree: payouts: %w", err)
	}

	seen := map[string]bool{}
	seenFirst := map[string]bool{}
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
			return false, fmt.Errorf("gobugfree: reports exceeded %d pages", maxPages)
		}
		var page reportListJSON
		u := fmt.Sprintf("/reports?size=%d&page=%d", reportPageSize, pageNo)
		if err := c.getJSON(ctx, u, &page); err != nil {
			_ = flush()
			return false, fmt.Errorf("gobugfree: reports page %d: %w", pageNo, err)
		}
		if len(page.Items) == 0 {
			break
		}
		if firstID := strings.TrimSpace(page.Items[0].ID); firstID != "" {
			if seenFirst[firstID] {
				break
			}
			seenFirst[firstID] = true
		}

		for _, item := range page.Items {
			id := strings.TrimSpace(item.ID)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true

			var payments []platform.ReportPayment
			if ps := byReportID[id]; len(ps) > 0 {
				payments = ps
			} else if t := strings.TrimSpace(item.Title); t != "" {
				payments = byTitle[t]
			}
			r := c.convertReport(item, payments, payoutsKnown)
			if r.SubmittedAt.IsZero() {
				continue
			}
			batch = append(batch, r)
			if len(batch) >= 25 {
				if err := flush(); err != nil {
					return false, err
				}
			}
		}

		if len(page.Items) < reportPageSize {
			break
		}
	}

	if err := flush(); err != nil {
		return false, err
	}
	return true, nil
}

func (c *Client) fetchPayouts(ctx context.Context) (byReportID map[string][]platform.ReportPayment, byTitle map[string][]platform.ReportPayment, known bool, err error) {
	byReportID = map[string][]platform.ReportPayment{}
	byTitle = map[string][]platform.ReportPayment{}

	var raw json.RawMessage
	if err := c.getJSON(ctx, "/payouts", &raw); err != nil {
		if errors.Is(err, errNotFound) {
			return byReportID, byTitle, false, nil
		}
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == 403 {
			return byReportID, byTitle, false, nil
		}
		return nil, nil, false, err
	}

	items := decodePayoutItems(raw)
	for i, it := range items {
		if it.Amount <= 0 {
			continue
		}
		cur := strings.ToUpper(strings.TrimSpace(it.Currency))
		if cur == "" {
			cur = "CHF"
		}
		kind := strings.ToLower(firstNonEmpty(it.Kind, it.Type, "bounty"))
		ref := strings.Trim(strings.TrimSpace(string(it.ID)), `"`)
		if ref == "" {
			ref = fmt.Sprintf("payout-%d", i+1)
		}
		awardedAt := parseTime(firstNonEmpty(it.AwardedAt, it.PaidAt, it.CreatedAt))
		paidAt := parseTime(it.PaidAt)
		var aw time.Time
		if awardedAt != nil {
			aw = *awardedAt
		}
		p := platform.ReportPayment{
			Ref:       ref,
			Kind:      kind,
			Amount:    it.Amount,
			Currency:  cur,
			AwardedAt: aw,
			PaidAt:    paidAt,
			Status:    strings.TrimSpace(it.Status),
		}
		rid := firstNonEmpty(it.ReportID, it.ReportIDAlt)
		if rid != "" {
			byReportID[rid] = append(byReportID[rid], p)
		} else if title := firstNonEmpty(it.ReportTitle, it.Title); title != "" {
			byTitle[title] = append(byTitle[title], p)
		}
	}
	return byReportID, byTitle, true, nil
}

func decodePayoutItems(raw json.RawMessage) []payoutItemJSON {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var direct []payoutItemJSON
	if json.Unmarshal(raw, &direct) == nil {
		return direct
	}
	var wrapped struct {
		Items []payoutItemJSON `json:"items"`
	}
	if json.Unmarshal(raw, &wrapped) == nil {
		return wrapped.Items
	}
	return nil
}

func (c *Client) convertReport(item reportItemJSON, payments []platform.ReportPayment, paymentsKnown bool) platform.Report {
	id := strings.TrimSpace(item.ID)
	orgSlug := strings.TrimSpace(item.OrgSlug)
	engSlug := strings.TrimSpace(item.EngagementSlug)

	progHandle := ""
	if orgSlug != "" && engSlug != "" {
		progHandle = makeHandle(orgSlug, engSlug)
	}
	progName := firstNonEmpty(item.EngagementTitle, item.OrgName, engSlug)
	if progName == "" && progHandle != "" {
		if m, ok := c.getMeta(progHandle); ok && m.Title != "" {
			progName = m.Title
		}
	}

	state, waitingOn := mapReportState(item.Status)
	sev := mapReportSeverity(item.Severity)
	subSev := mapReportSeverity(item.SeveritySubmitted)

	submittedAt := parseTime(item.SubmittedAt)
	updatedAt := parseTime(item.UpdatedAt)

	var firstBountyAt *time.Time
	totalPaid := 0.0
	for i := range payments {
		totalPaid += payments[i].Amount
		if !payments[i].AwardedAt.IsZero() && (firstBountyAt == nil || payments[i].AwardedAt.Before(*firstBountyAt)) {
			t := payments[i].AwardedAt
			firstBountyAt = &t
		}
	}
	if firstBountyAt != nil {
		for i := range payments {
			if payments[i].AwardedAt.IsZero() {
				payments[i].AwardedAt = *firstBountyAt
			}
		}
	} else if len(payments) > 0 && updatedAt != nil {
		t := *updatedAt
		firstBountyAt = &t
		for i := range payments {
			if payments[i].AwardedAt.IsZero() {
				payments[i].AwardedAt = t
			}
		}
	}

	var closedAt *time.Time
	if isTerminalReportState(state) && updatedAt != nil {
		closedAt = updatedAt
	}

	var cvss *float64
	if item.CVSSScore != nil && *item.CVSSScore > 0 {
		v := *item.CVSSScore
		cvss = &v
	}

	r := platform.Report{
		Key:               id,
		ProgramHandle:     progHandle,
		ProgramName:       progName,
		Title:             strings.TrimSpace(item.Title),
		URL:               c.base.String() + "/reports/" + url.PathEscape(id),
		State:             state,
		RawState:          strings.TrimSpace(item.Status),
		Severity:          sev,
		SubmittedSeverity: subSev,
		SeverityChanged:   subSev != "" && sev != "" && subSev != sev,
		CVSS:              cvss,
		FirstBountyAt:     firstBountyAt,
		ClosedAt:          closedAt,
		LastActivityAt:    updatedAt,
		WaitingOn:         waitingOn,
		Detailed:          true,
		PaymentsKnown:     paymentsKnown,
		Payments:          payments,
		Marker:            fmt.Sprintf("%s|%s|%.2f|%s", item.Status, sev, totalPaid, item.UpdatedAt),
	}
	if submittedAt != nil {
		r.SubmittedAt = *submittedAt
	}

	// Allow-list metadata fields only (never write-ups or comments).
	r.Raw, _ = json.Marshal(map[string]any{
		"id":                id,
		"orgSlug":           orgSlug,
		"engagementSlug":    engSlug,
		"status":            item.Status,
		"severity":          item.Severity,
		"severitySubmitted": item.SeveritySubmitted,
		"cvssScore":         item.CVSSScore,
		"submittedAt":       item.SubmittedAt,
		"updatedAt":         item.UpdatedAt,
	})
	return r
}

func mapReportState(status string) (state, waitingOn string) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "new", "draft":
		return platform.ReportNew, "program"
	case "triaging", "reopened":
		return platform.ReportTriaged, "program"
	case "validated", "accepted":
		return platform.ReportAccepted, "program"
	case "fixed", "closed", "resolved":
		return platform.ReportResolved, ""
	case "duplicate":
		return platform.ReportDuplicate, ""
	case "out_of_scope":
		return platform.ReportOutOfScope, ""
	case "informative":
		return platform.ReportInformative, ""
	case "spam":
		return platform.ReportSpam, ""
	case "accepted_risk":
		return platform.ReportAcceptedRisk, ""
	case "rejected":
		return platform.ReportNotApplicable, ""
	default:
		return platform.ReportNew, ""
	}
}

func isTerminalReportState(state string) bool {
	switch state {
	case platform.ReportResolved, platform.ReportDuplicate, platform.ReportOutOfScope,
		platform.ReportInformative, platform.ReportNotApplicable, platform.ReportSpam, platform.ReportAcceptedRisk:
		return true
	default:
		return false
	}
}

func mapReportSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "none", "info", "informational":
		return platform.SeverityNone
	case "low":
		return platform.SeverityLow
	case "medium":
		return platform.SeverityMedium
	case "high":
		return platform.SeverityHigh
	case "critical":
		return platform.SeverityCritical
	default:
		return ""
	}
}
