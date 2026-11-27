package inspectiv

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
)

type submissionsPageJSON struct {
	Next       *string              `json:"next"`
	Previous   *string              `json:"previous"`
	Count      int                  `json:"count"`
	TotalPages int                  `json:"total_pages"`
	Results    []submissionItemJSON `json:"results"`
}

type submissionProgramJSON struct {
	UUID  string `json:"uuid"`
	Title string `json:"title"`
}

type submissionVulnTypeJSON struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

// submissionItemJSON captures metadata-only fields from /submissions/list/.
// HuntDeck NEVER calls /submissions/{uuid}/ or /threads/{uuid}/.
type submissionItemJSON struct {
	UUID              string                   `json:"uuid"`
	Title             string                   `json:"title"`
	Priority          int                      `json:"priority"`
	PriorityDisplay   string                   `json:"priority_display"`
	Status            int                      `json:"status"`
	StatusDisplay     string                   `json:"status_display"`
	Resolution        int                      `json:"resolution"`
	ResolutionDisplay string                   `json:"resolution_display"`
	Payout            float64                  `json:"payout"`
	PayoutDisplay     string                   `json:"payout_display"`
	BountyStatus      string                   `json:"bounty_status"`
	DateCreated       string                   `json:"date_created"`
	Program           submissionProgramJSON    `json:"program"`
	VulnerabilityType []submissionVulnTypeJSON `json:"vulnerability_types"`
}

// ReportsConfigured implements platform.ReportFetcher.
func (c *Client) ReportsConfigured() bool { return c.authenticated() }

// FetchReports implements platform.ReportFetcher.
// It paginates GET /submissions/list/?page=1..N (omitting start_date and
// end_date) and NEVER fetches /submissions/{uuid}/ or /threads/{uuid}/.
func (c *Client) FetchReports(ctx context.Context, _ map[string]string, _ int, save platform.ReportsSaver) (bool, error) {
	if !c.authenticated() {
		return false, platform.ErrNoWebLogin
	}
	if err := c.ensureSession(ctx); err != nil {
		return false, fmt.Errorf("inspectiv: reports: %w", err)
	}

	seen := map[string]bool{}
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
			return false, fmt.Errorf("inspectiv: reports exceeded %d pages", maxPages)
		}
		var page submissionsPageJSON
		u := fmt.Sprintf("/submissions/list/?page=%d", pageNo)
		if err := c.getJSON(ctx, u, &page); err != nil {
			_ = flush()
			return false, fmt.Errorf("inspectiv: reports page %d: %w", pageNo, err)
		}

		for _, item := range page.Results {
			uuid := strings.TrimSpace(item.UUID)
			if uuid == "" || seen[uuid] {
				continue
			}
			seen[uuid] = true
			r := c.convertSubmission(item)
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

		if len(page.Results) == 0 {
			break
		}
		if page.TotalPages > 0 && pageNo >= page.TotalPages && page.Next == nil {
			break
		}
		if page.TotalPages == 0 && page.Next == nil {
			break
		}
	}

	if err := flush(); err != nil {
		return false, err
	}
	return true, nil
}

func (c *Client) convertSubmission(item submissionItemJSON) platform.Report {
	key := strings.TrimSpace(item.UUID)
	state, waitingOn := mapSubmissionState(item.Status, item.Resolution)
	sev, sevLabel := mapPriority(item.Priority, item.PriorityDisplay)

	rawState := buildRawState(item.Status, item.StatusDisplay, item.Resolution, item.ResolutionDisplay)
	submitted := parseTime(item.DateCreated)

	var payments []platform.ReportPayment
	if item.Payout > 0 {
		p := platform.ReportPayment{
			Ref:      key + "-bounty",
			Kind:     "bounty",
			Amount:   item.Payout,
			Currency: currencyUSD,
			Status:   strings.TrimSpace(item.BountyStatus),
		}
		if submitted != nil {
			p.AwardedAt = *submitted
		}
		payments = append(payments, p)
	}

	r := platform.Report{
		Key:           key,
		ProgramHandle: strings.TrimSpace(item.Program.UUID),
		ProgramName:   strings.TrimSpace(item.Program.Title),
		Title:         strings.TrimSpace(item.Title),
		URL:           strings.TrimSuffix(c.web.String(), "/") + "/#/submissions/" + url.PathEscape(key),
		State:         state,
		RawState:      rawState,
		Severity:      sev,
		SeverityLabel: sevLabel,
		WaitingOn:     waitingOn,
		Detailed:      true,
		PaymentsKnown: true,
		Payments:      payments,
		Marker:        fmt.Sprintf("%d|%d|%d|%.2f|%s", item.Status, item.Resolution, item.Priority, item.Payout, strings.TrimSpace(item.BountyStatus)),
	}
	if submitted != nil {
		r.SubmittedAt = *submitted
	}

	r.Raw, _ = json.Marshal(map[string]any{
		"uuid":               key,
		"priority":           item.Priority,
		"priority_display":   item.PriorityDisplay,
		"status":             item.Status,
		"status_display":     item.StatusDisplay,
		"resolution":         item.Resolution,
		"resolution_display": item.ResolutionDisplay,
		"payout":             item.Payout,
		"bounty_status":      item.BountyStatus,
		"program_uuid":       item.Program.UUID,
	})
	return r
}

func mapSubmissionState(status, resolution int) (state, waitingOn string) {
	switch resolution {
	case 2: // Accepted
		if status == 3 {
			return platform.ReportResolved, ""
		}
		return platform.ReportAccepted, "program"
	case 4: // Duplicate
		return platform.ReportDuplicate, ""
	case 3: // Rejected
		return platform.ReportNotApplicable, ""
	}

	switch status {
	case 1: // New
		return platform.ReportNew, "program"
	case 2, 4: // In review, Validating
		return platform.ReportTriaged, "program"
	case 3: // Closed without Accepted/Rejected/Duplicate
		return platform.ReportInformative, ""
	default:
		return platform.ReportNew, ""
	}
}

func buildRawState(status int, statusDisplay string, resolution int, resolutionDisplay string) string {
	sd := strings.TrimSpace(statusDisplay)
	if sd == "" {
		switch status {
		case 1:
			sd = "New"
		case 2:
			sd = "In review"
		case 3:
			sd = "Closed"
		case 4:
			sd = "Validating"
		}
	}
	rd := strings.TrimSpace(resolutionDisplay)
	if rd == "" {
		switch resolution {
		case 1:
			rd = "Unresolved"
		case 2:
			rd = "Accepted"
		case 3:
			rd = "Rejected"
		case 4:
			rd = "Duplicate"
		}
	}
	if rd != "" && !strings.EqualFold(rd, "Unresolved") {
		if sd != "" && !strings.EqualFold(sd, rd) {
			return sd + " (" + rd + ")"
		}
		return rd
	}
	return sd
}

func mapPriority(priority int, display string) (sev, label string) {
	label = strings.TrimSpace(display)
	switch priority {
	case 0:
		sev = platform.SeverityCritical
		if label == "" {
			label = "Critical"
		}
	case 1:
		sev = platform.SeverityHigh
		if label == "" {
			label = "High"
		}
	case 2:
		sev = platform.SeverityMedium
		if label == "" {
			label = "Medium"
		}
	case 3:
		sev = platform.SeverityLow
		if label == "" {
			label = "Low"
		}
	case 4:
		sev = platform.SeverityNone
		if label == "" {
			label = "Informational"
		}
	case 5:
		sev = platform.SeverityNone
		if label == "" {
			label = "None"
		}
	}
	return sev, label
}
