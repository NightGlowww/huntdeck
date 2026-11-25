package bugcrowd

// The account's own reports (platform.ReportFetcher), from the JSON the
// web app's Submissions page reads (logged in; verified 2026-11-22):
//
//   - GET /submissions.json?sort[]=submitted-desc&draft[]=false&offset[]=N,
//     25 a page until meta.totalHits. The offset only works array-style
//     (offset[]=): plain offset= or page= give the first page again.
//   - For each report whose change marker moved since the last read:
//     GET /submissions/{uuid} (JSON with Accept: application/json; status,
//     reward, close date, and the activity feed's address), then that feed
//     (/{program}/submissions/{ref}/activities, newest first): state and
//     priority changes, rewards with their dates, who commented when.
//
// So the first run costs two requests per report (spread over runs by
// maxDetails), later ones only the changed reports'. At BC_RATE.
//
// Bugcrowd's feed gives the new priority when triage changes it, never the
// old one: a report whose priority was changed has no known submitted
// severity unless the user enters it. Never kept: the description, the
// bug URL, comment text, and anyone's name or email.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// ReportsConfigured implements platform.ReportFetcher: reports need a
// login or a session.
func (c *Client) ReportsConfigured() bool { return c.authenticated() }

type bcSubmissionJSON struct {
	SubmissionURL       string  `json:"submission_url"`
	ReferenceNumber     string  `json:"reference_number"`
	Title               string  `json:"title"`
	ProgramName         string  `json:"program_name"`
	EngagementName      string  `json:"engagement_name"`
	Substate            string  `json:"substate"`
	Duplicate           bool    `json:"duplicate"`
	Priority            *int    `json:"priority"`
	Points              *int    `json:"points"`
	CommentCount        int     `json:"comment_count"`
	SubmittedAt         *string `json:"submitted_at"`
	LastActivityDate    *string `json:"last_activity_date"`
	ResearcherUpdatedAt *string `json:"researcher_updated_at"`
	Draft               bool    `json:"draft"`
	RarOnCurrentUser    bool    `json:"rar_on_current_user"`
	BlockedByCurrent    bool    `json:"blocked_by_current_user"`
}

type bcDetailJSON struct {
	SubmissionHeader struct {
		ClosedAt *string `json:"closedAt"`
		Reward   *struct {
			Amount *string `json:"amount"`
		} `json:"reward"`
	} `json:"submissionHeader"`
	SubmissionData struct {
		ActivityFeedURL string `json:"activityFeedUrl"`
	} `json:"submissionData"`
}

type bcActivityJSON struct {
	Key               string  `json:"key"`
	UUID              string  `json:"uuid"`
	CreatedAt         *string `json:"created_at"`
	CreatedByStaff    bool    `json:"created_by_staff"`
	CreatedByCustomer bool    `json:"created_by_customer"`
	Params            struct {
		Substate *string `json:"substate"`
		Priority *int    `json:"priority"`
		Amount   any     `json:"amount"`
	} `json:"params"`
}

// FetchReports implements platform.ReportFetcher.
func (c *Client) FetchReports(ctx context.Context, known map[string]string, maxDetails int, save platform.ReportsSaver) (bool, error) {
	if !c.authenticated() {
		return false, ErrSessionExpired
	}
	if err := c.ensureSession(ctx); err != nil {
		return false, fmt.Errorf("bugcrowd: reports: %w", err)
	}
	var list []bcSubmissionJSON
	total := -1
	for page := 1; page <= maxPages && (total < 0 || len(list) < total); page++ {
		var p struct {
			Submissions []bcSubmissionJSON `json:"submissions"`
			Meta        struct {
				TotalHits int `json:"totalHits"`
			} `json:"meta"`
		}
		u := fmt.Sprintf("%s/submissions.json?sort%%5B%%5D=submitted-desc&draft%%5B%%5D=false&offset%%5B%%5D=%d", c.base, len(list))
		if err := c.getJSON(ctx, u, &p); err != nil {
			return false, fmt.Errorf("bugcrowd: list reports page %d: %w", page, err)
		}
		total = p.Meta.TotalHits
		if len(p.Submissions) == 0 {
			break
		}
		if len(list) > 0 && p.Submissions[0].SubmissionURL == list[0].SubmissionURL {
			return false, fmt.Errorf("bugcrowd: list reports page %d: the offset was ignored", page)
		}
		list = append(list, p.Submissions...)
	}
	complete := total >= 0 && len(list) >= total
	var batch []platform.Report
	details := 0
	var detailErr error
	for _, s := range list {
		r := convertBCSubmission(s)
		if r.Key == "" || r.SubmittedAt.IsZero() || s.Draft {
			continue
		}
		if known[r.Key] != r.Marker && details < maxDetails && detailErr == nil {
			details++
			if err := c.addBCDetail(ctx, &r); err != nil {
				if ctx.Err() != nil {
					return false, err
				}
				detailErr = fmt.Errorf("bugcrowd: report %s: %w", r.Key, err)
			}
		}
		batch = append(batch, r)
		if len(batch) == 25 {
			if err := save(ctx, batch); err != nil {
				return false, err
			}
			batch = nil
		}
	}
	if len(batch) > 0 {
		if err := save(ctx, batch); err != nil {
			return false, err
		}
	}
	if !complete {
		return false, errors.Join(detailErr, fmt.Errorf("bugcrowd: listed %d of %d reports", len(list), total))
	}
	return true, detailErr
}

var bcSubmissionPath = regexp.MustCompile(`/submissions/([0-9a-fA-F-]{8,})/?$`)

// bugcrowdStates maps a substate to HuntDeck's; a duplicate is one
// whatever the substate (which follows the original).
var bugcrowdStates = map[string]string{
	"nue":              platform.ReportNew,
	"new":              platform.ReportNew,
	"triaged":          platform.ReportTriaged,
	"unresolved":       platform.ReportAccepted,
	"resolved":         platform.ReportResolved,
	"wont_fix":         platform.ReportAcceptedRisk,
	"informational":    platform.ReportInformative,
	"not_applicable":   platform.ReportNotApplicable,
	"not_reproducible": platform.ReportNotApplicable,
	"out_of_scope":     platform.ReportOutOfScope,
}

func bcState(substate string, duplicate bool) string {
	if duplicate {
		return platform.ReportDuplicate
	}
	if s, ok := bugcrowdStates[substate]; ok {
		return s
	}
	return platform.ReportNew
}

func bcPriority(p *int) (sev, label string) {
	if p == nil || *p < 1 || *p > 5 {
		return "", ""
	}
	return []string{"", platform.SeverityCritical, platform.SeverityHigh, platform.SeverityMedium, platform.SeverityLow,
		platform.SeverityNone}[*p], "P" + strconv.Itoa(*p)
}

func bcTime(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil
	}
	t = t.UTC()
	return &t
}

func convertBCSubmission(s bcSubmissionJSON) platform.Report {
	m := bcSubmissionPath.FindStringSubmatch(s.SubmissionURL)
	if m == nil {
		return platform.Report{}
	}
	name := s.EngagementName
	if name == "" {
		name = s.ProgramName
	}
	r := platform.Report{
		Key:            m[1],
		ProgramName:    name,
		Title:          s.Title,
		URL:            defaultBaseURL + "/submissions/" + m[1],
		State:          bcState(s.Substate, s.Duplicate),
		RawState:       s.Substate,
		Comments:       s.CommentCount,
		LastActivityAt: bcTime(s.LastActivityDate),
	}
	if s.Duplicate {
		r.RawState += " (duplicate)"
	}
	r.Severity, r.SeverityLabel = bcPriority(s.Priority)
	if t := bcTime(s.SubmittedAt); t != nil {
		r.SubmittedAt = *t
	}
	if s.RarOnCurrentUser || s.BlockedByCurrent {
		r.WaitingOn = "you"
	}
	pr, pts := 0, 0
	if s.Priority != nil {
		pr = *s.Priority
	}
	if s.Points != nil {
		pts = *s.Points
	}
	r.Marker = fmt.Sprintf("%s|%t|%d|%d|%d|%s", s.Substate, s.Duplicate, pr, pts, s.CommentCount, str(s.LastActivityDate))
	r.Raw, _ = json.Marshal(map[string]any{"substate": s.Substate, "duplicate": s.Duplicate, "priority": s.Priority, "points": s.Points})
	return r
}

// addBCDetail reads a report's page JSON and its activity feed.
func (c *Client) addBCDetail(ctx context.Context, r *platform.Report) error {
	var d bcDetailJSON
	if err := c.getJSON(ctx, c.base.String()+"/submissions/"+url.PathEscape(r.Key), &d); err != nil {
		return err
	}
	feed, err := c.sameSite(d.SubmissionData.ActivityFeedURL)
	if err != nil {
		return err
	}
	var f struct {
		Activities []bcActivityJSON `json:"activities"`
	}
	if err := c.getJSON(ctx, feed, &f); err != nil {
		return fmt.Errorf("activities: %w", err)
	}
	r.Detailed, r.PaymentsKnown = true, true
	r.ClosedAt = bcTime(d.SubmissionHeader.ClosedAt)
	acts := slices.Clone(f.Activities)
	slices.SortStableFunc(acts, func(a, b bcActivityJSON) int { return strings.Compare(str(a.CreatedAt), str(b.CreatedAt)) })
	changed := false
	var lastMove *time.Time // the last change of state
	lastComment := ""
	prevState := ""
	for _, a := range acts {
		at := bcTime(a.CreatedAt)
		if at == nil {
			continue
		}
		program := a.CreatedByStaff || a.CreatedByCustomer
		if program {
			if r.FirstResponseAt == nil {
				r.FirstResponseAt = at
			}
			r.LastProgramActivityAt = at
		}
		switch a.Key {
		case "comment.created":
			kind := platform.EventCommentYou
			if program {
				kind, lastComment = platform.EventCommentProgram, "program"
			} else {
				lastComment = "you"
				r.LastHunterActivityAt = at
			}
			r.Events = append(r.Events, platform.ReportEvent{At: *at, Kind: kind})
		case "submission.transitioned":
			to := str(a.Params.Substate)
			r.Events = append(r.Events, platform.ReportEvent{At: *at, Kind: platform.EventState, From: prevState, To: to})
			prevState, lastMove = to, at
			if (to == "triaged" || to == "unresolved") && r.TriagedAt == nil {
				r.TriagedAt = at
			}
		case "submission.duplicated":
			r.Events = append(r.Events, platform.ReportEvent{At: *at, Kind: platform.EventState, From: prevState, To: "duplicate"})
			lastMove = at
		case "submission.priority_set", "submission.vrt_updated":
			if a.Params.Priority == nil {
				continue
			}
			changed = true
			sev, _ := bcPriority(a.Params.Priority)
			r.Events = append(r.Events, platform.ReportEvent{At: *at, Kind: platform.EventSeverity, To: sev})
		case "submission.reward_created":
			amount, cur, ok := bcMoney(a.Params.Amount)
			if !ok {
				continue
			}
			r.Payments = append(r.Payments, platform.ReportPayment{Ref: a.UUID, Kind: "bounty", Amount: amount, Currency: cur, AwardedAt: *at})
			r.Events = append(r.Events, platform.ReportEvent{At: *at, Kind: platform.EventBounty, To: "bounty"})
			if r.FirstBountyAt == nil {
				r.FirstBountyAt = at
			}
		}
	}
	if len(r.Payments) == 0 && d.SubmissionHeader.Reward != nil {
		// The feed's first page didn't reach the reward: the total, dated
		// when the report closed (or was last active).
		if amount, cur, ok := bcMoney(str(d.SubmissionHeader.Reward.Amount)); ok {
			at := r.ClosedAt
			if at == nil {
				at = r.LastActivityAt
			}
			if at != nil {
				r.Payments = append(r.Payments, platform.ReportPayment{Ref: "total", Kind: "bounty", Amount: amount, Currency: cur, AwardedAt: *at})
				r.FirstBountyAt = at
			}
		}
	}
	if r.ClosedAt == nil && lastMove != nil && r.State != platform.ReportNew && r.State != platform.ReportTriaged && r.State != platform.ReportAccepted {
		r.ClosedAt = lastMove // the header has no close date for most rejected reports
	}
	r.SeverityChanged = changed
	if !changed {
		r.SubmittedSeverity = r.Severity
	}
	// Waiting on you only when Bugcrowd says so (a request for a response,
	// or a blocker on you); a staff comment may need nothing back.
	if r.WaitingOn == "" && lastComment == "you" && (r.State == platform.ReportNew || r.State == platform.ReportTriaged || r.State == platform.ReportAccepted) {
		r.WaitingOn = "program"
	}
	slices.SortStableFunc(r.Events, func(a, b platform.ReportEvent) int { return a.At.Compare(b.At) })
	return nil
}

// sameSite checks an address Bugcrowd gave before the session goes to it.
func (c *Client) sameSite(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("no activity feed address")
	}
	u, err := c.base.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("activity feed address: %w", err)
	}
	if u.Scheme != c.base.Scheme || u.Host != c.base.Host {
		return "", fmt.Errorf("activity feed on unexpected host %q", u.Host)
	}
	return u.String(), nil
}

var bcCurrencies = map[string]string{"$": "USD", "€": "EUR", "£": "GBP", "A$": "AUD", "C$": "CAD", "CA$": "CAD", "AU$": "AUD"}

// bcMoney reads an amount as Bugcrowd shows it ("$1,234.50", "€500").
// Amounts in a currency it can't tell are skipped rather than guessed.
func bcMoney(v any) (float64, string, bool) {
	s, ok := v.(string)
	if !ok {
		return 0, "", false
	}
	s = strings.TrimSpace(s)
	i := strings.IndexFunc(s, func(r rune) bool { return r >= '0' && r <= '9' })
	if i <= 0 {
		return 0, "", false
	}
	cur, ok := bcCurrencies[strings.TrimSpace(s[:i])]
	if !ok {
		if code := strings.TrimSpace(s[:i]); len(code) == 3 && strings.ToUpper(code) == code {
			cur, ok = code, true
		}
	}
	n, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s[i:]), ",", ""), 64)
	if !ok || err != nil || n <= 0 {
		return 0, "", false
	}
	return n, cur, true
}

func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
