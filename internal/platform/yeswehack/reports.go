package yeswehack

// The account's own reports (platform.ReportFetcher), from the YesWeHack API:
//
//   - POST /v2/hunter/reports?page={page}&resultsPerPage=100 (JSON `{}`),
//     every page: states, severities, dates, reward cents and unread flag.
//   - GET /collaborator/reports?page={page}&resultsPerPage=100, every page
//     when enabled (HTTP 403/404 ignored gracefully), deduplicated by key.
//   - GET /reports/{id} and GET /reports/{id}/logs?filter-bug-tracker-logs=1
//     for each report whose change marker moved since the last read: close/triage
//     dates, duplicate reference, state/severity/reward/comment events.
//
// Never kept: description, PoC, message bodies, attachments, IP addresses, or
// personal names.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

var _ platform.ReportFetcher = (*Client)(nil)

const (
	reportPageSize = 100
	reportWebBase  = "https://yeswehack.com/reports/"
)

// ReportsConfigured implements platform.ReportFetcher: reports need a login or
// a non-empty bearer token.
func (c *Client) ReportsConfigured() bool {
	if c.email != "" {
		return true
	}
	c.authMu.Lock()
	defer c.authMu.Unlock()
	return c.token != ""
}

type reportStatusJSON struct {
	WorkflowState string `json:"workflow_state"`
}

type reportCVSSJSON struct {
	Criticity string   `json:"criticity"`
	Score     *float64 `json:"score"`
}

type reportProgramJSON struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

type reportJSON struct {
	ID         json.Number        `json:"id"`
	LocalID    string             `json:"local_id"`
	Title      string             `json:"title"`
	Program    *reportProgramJSON `json:"program"`
	Status     *reportStatusJSON  `json:"status"`
	CVSS       *reportCVSSJSON    `json:"cvss"`
	Reward     *int64             `json:"reward"`
	Currency   string             `json:"currency"`
	MarkedAs   string             `json:"marked_as"`
	CreatedAt  *string            `json:"created_at"`
	ChangedAt  *string            `json:"changed_at"`
	AcceptedAt *string            `json:"accepted_at"`
	ResolvedAt *string            `json:"resolved_at"`
	ClosedAt   *string            `json:"closed_at"`
}

type reportDetailJSON struct {
	Status      *reportStatusJSON `json:"status"`
	CVSS        *reportCVSSJSON   `json:"cvss"`
	Reward      *int64            `json:"reward"`
	Currency    string            `json:"currency"`
	AcceptedAt  *string           `json:"accepted_at"`
	ResolvedAt  *string           `json:"resolved_at"`
	ClosedAt    *string           `json:"closed_at"`
	DuplicateOf json.RawMessage   `json:"duplicate_of"`
}

type logActorJSON struct {
	Role string `json:"role"`
	Type string `json:"type"`
}

type reportLogJSON struct {
	ID               json.Number     `json:"id"`
	CreatedAt        *string         `json:"created_at"`
	Type             string          `json:"type"`
	Private          bool            `json:"private"`
	Author           *logActorJSON   `json:"author"`
	User             *logActorJSON   `json:"user"`
	AuthorType       string          `json:"author_type"`
	UserType         string          `json:"user_type"`
	IsHunter         *bool           `json:"is_hunter"`
	OldStatus        json.RawMessage `json:"old_status"`
	NewStatus        json.RawMessage `json:"new_status"`
	Status           json.RawMessage `json:"status"`
	OldWorkflowState string          `json:"old_workflow_state"`
	NewWorkflowState string          `json:"new_workflow_state"`
	OldCVSS          *reportCVSSJSON `json:"old_cvss"`
	NewCVSS          *reportCVSSJSON `json:"new_cvss"`
	CVSS             *reportCVSSJSON `json:"cvss"`
	Reward           json.RawMessage `json:"reward"`
	Amount           *int64          `json:"amount"`
	Currency         string          `json:"currency"`
	RewardType       string          `json:"reward_type"`
	BountyType       string          `json:"bounty_type"`
	From             json.RawMessage `json:"from"`
	To               json.RawMessage `json:"to"`
}

type reportListPageJSON struct {
	Items      []reportJSON `json:"items"`
	Pagination struct {
		NbPages int `json:"nb_pages"`
	} `json:"pagination"`
}

type listedReport struct {
	report      platform.Report
	rewardCents int64
	currency    string
}

// FetchReports implements platform.ReportFetcher.
func (c *Client) FetchReports(ctx context.Context, known map[string]string, maxDetails int, save platform.ReportsSaver) (bool, error) {
	if !c.ReportsConfigured() {
		return false, ErrAuth
	}
	hunterItems, hunterTrunc, err := c.listReportPages(ctx, func(page int, dst *reportListPageJSON) error {
		u := fmt.Sprintf("%s/v2/hunter/reports?page=%d&resultsPerPage=%d", c.base, page, reportPageSize)
		return c.postJSON(ctx, u, struct{}{}, dst)
	})
	if err != nil {
		return false, fmt.Errorf("yeswehack: list reports: %w", err)
	}
	collabItems, collabTrunc, err := c.listReportPages(ctx, func(page int, dst *reportListPageJSON) error {
		u := fmt.Sprintf("%s/collaborator/reports?page=%d&resultsPerPage=%d", c.base, page, reportPageSize)
		return c.getJSON(ctx, u, dst)
	})
	if err != nil && !errors.Is(err, platform.ErrScopeUnavailable) && !errors.Is(err, errNotFound) {
		return false, fmt.Errorf("yeswehack: list collaborator reports: %w", err)
	}

	seen := make(map[string]bool, len(hunterItems)+len(collabItems))
	var list []listedReport
	for _, rj := range append(hunterItems, collabItems...) {
		lr := convertReport(rj)
		if lr.report.Key == "" || lr.report.SubmittedAt.IsZero() || seen[lr.report.Key] {
			continue
		}
		seen[lr.report.Key] = true
		list = append(list, lr)
	}

	slices.SortStableFunc(list, func(a, b listedReport) int {
		return b.report.SubmittedAt.Compare(a.report.SubmittedAt)
	})

	var batch []platform.Report
	details := 0
	var detailErr error
	for _, lr := range list {
		r := lr.report
		if known[r.Key] != r.Marker && details < maxDetails && detailErr == nil {
			details++
			detail, logs, err := c.fetchReportDetails(ctx, r.Key)
			if err != nil {
				if ctx.Err() != nil {
					return false, err
				}
				detailErr = fmt.Errorf("yeswehack: report %s: %w", r.Key, err)
			} else {
				addReportDetail(&r, detail, logs, lr.rewardCents, lr.currency)
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
	return !hunterTrunc && !collabTrunc, detailErr
}

func (c *Client) listReportPages(ctx context.Context, fetch func(page int, dst *reportListPageJSON) error) ([]reportJSON, bool, error) {
	var items []reportJSON
	pages := 1
	truncated := false
	for page := 1; page <= pages; page++ {
		if page > maxPages {
			truncated = true
			break
		}
		var p reportListPageJSON
		if err := fetch(page, &p); err != nil {
			return nil, false, err
		}
		items = append(items, p.Items...)
		if page == 1 {
			pages = p.Pagination.NbPages
			if pages > maxPages {
				pages = maxPages
				truncated = true
			}
		}
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
	}
	return items, truncated, nil
}

func (c *Client) fetchReportDetails(ctx context.Context, id string) (reportDetailJSON, []reportLogJSON, error) {
	escaped := url.PathEscape(id)
	var detail reportDetailJSON
	if err := c.getJSON(ctx, c.base.String()+"/reports/"+escaped, &detail); err != nil {
		return detail, nil, err
	}
	var logs []reportLogJSON
	pages := 1
	for page := 1; page <= pages && page <= maxPages; page++ {
		u := c.base.String() + "/reports/" + escaped + "/logs?filter-bug-tracker-logs=1"
		if page > 1 {
			u += "&page=" + strconv.Itoa(page)
		}
		var p struct {
			Items      []reportLogJSON `json:"items"`
			Pagination struct {
				NbPages int `json:"nb_pages"`
			} `json:"pagination"`
		}
		if err := c.getJSON(ctx, u, &p); err != nil {
			return detail, nil, err
		}
		logs = append(logs, p.Items...)
		if page == 1 && p.Pagination.NbPages > 1 {
			pages = p.Pagination.NbPages
		}
	}
	return detail, logs, nil
}

var ywhStates = map[string]string{
	"new":            platform.ReportNew,
	"more_info":      platform.ReportNew,
	"under_review":   platform.ReportTriaged,
	"accepted":       platform.ReportAccepted,
	"ask_verif":      platform.ReportAccepted,
	"resolved":       platform.ReportResolved,
	"wont_fix":       platform.ReportAcceptedRisk,
	"rtfs":           platform.ReportAcceptedRisk,
	"duplicate":      platform.ReportDuplicate,
	"informative":    platform.ReportInformative,
	"out_of_scope":   platform.ReportOutOfScope,
	"spam":           platform.ReportSpam,
	"not_applicable": platform.ReportNotApplicable,
	"invalid":        platform.ReportNotApplicable,
	"auto_close":     platform.ReportNotApplicable,
}

func ywhReportState(workflowState string) string {
	if st, ok := ywhStates[workflowState]; ok {
		return st
	}
	return platform.ReportNew
}

func ywhSeverity(criticity string) string {
	switch strings.ToLower(strings.TrimSpace(criticity)) {
	case platform.SeverityNone:
		return platform.SeverityNone
	case platform.SeverityLow:
		return platform.SeverityLow
	case platform.SeverityMedium:
		return platform.SeverityMedium
	case platform.SeverityHigh:
		return platform.SeverityHigh
	case platform.SeverityCritical:
		return platform.SeverityCritical
	}
	return ""
}

func isOpenReportState(state string) bool {
	return state == platform.ReportNew || state == platform.ReportTriaged || state == platform.ReportAccepted
}

func isClosedWorkflowState(ws string) bool {
	st, ok := ywhStates[ws]
	return ok && !isOpenReportState(st)
}

func convertReport(rj reportJSON) listedReport {
	key := strings.TrimSpace(rj.ID.String())
	var ws string
	if rj.Status != nil {
		ws = rj.Status.WorkflowState
	}
	var progHandle, progName string
	if rj.Program != nil {
		progHandle = rj.Program.Slug
		progName = strings.TrimSpace(rj.Program.Title)
	}
	var criticity, sev string
	var cvssScore *float64
	if rj.CVSS != nil {
		criticity = rj.CVSS.Criticity
		sev = ywhSeverity(criticity)
		cvssScore = rj.CVSS.Score
	}
	var rewardCents int64
	rewardMarker := ""
	if rj.Reward != nil {
		rewardCents = *rj.Reward
		rewardMarker = strconv.FormatInt(rewardCents, 10)
	}
	currency := strings.ToUpper(strings.TrimSpace(rj.Currency))
	if currency == "" {
		currency = "EUR"
	}

	r := platform.Report{
		Key:            key,
		ProgramHandle:  progHandle,
		ProgramName:    progName,
		Title:          rj.Title,
		URL:            reportWebBase + key,
		State:          ywhReportState(ws),
		RawState:       ws,
		Severity:       sev,
		CVSS:           cvssScore,
		TriagedAt:      parseTime(rj.AcceptedAt),
		ClosedAt:       parseTime(rj.ClosedAt),
		LastActivityAt: parseTime(rj.ChangedAt),
		Unread:         rj.MarkedAs == "U",
	}
	if r.ClosedAt == nil {
		r.ClosedAt = parseTime(rj.ResolvedAt)
	}
	if t := parseTime(rj.CreatedAt); t != nil {
		r.SubmittedAt = *t
	}
	if ws == "more_info" {
		r.WaitingOn = "you"
	}
	r.Marker = strings.Join([]string{ws, criticity, rewardMarker, deref(rj.ChangedAt), rj.MarkedAs}, "|")
	r.Raw, _ = json.Marshal(map[string]any{
		"local_id":       rj.LocalID,
		"workflow_state": ws,
		"criticity":      criticity,
		"score":          cvssScore,
		"reward":         rj.Reward,
		"currency":       currency,
	})
	return listedReport{report: r, rewardCents: rewardCents, currency: currency}
}

func addReportDetail(r *platform.Report, d reportDetailJSON, logs []reportLogJSON, rewardCents int64, currency string) {
	if d.Status != nil && d.Status.WorkflowState != "" {
		ws := d.Status.WorkflowState
		r.State = ywhReportState(ws)
		r.RawState = ws
		if ws == "more_info" {
			r.WaitingOn = "you"
		} else {
			r.WaitingOn = ""
		}
	}
	if d.CVSS != nil {
		if sev := ywhSeverity(d.CVSS.Criticity); sev != "" {
			r.Severity = sev
		}
		if d.CVSS.Score != nil {
			r.CVSS = d.CVSS.Score
		}
	}
	if d.Reward != nil {
		rewardCents = *d.Reward
	}
	if cur := strings.ToUpper(strings.TrimSpace(d.Currency)); cur != "" {
		currency = cur
	}
	if currency == "" {
		currency = "EUR"
	}
	if t := parseTime(d.AcceptedAt); t != nil && (r.TriagedAt == nil || t.Before(*r.TriagedAt)) {
		r.TriagedAt = t
	}
	if t := parseTime(d.ClosedAt); t != nil {
		r.ClosedAt = t
	} else if r.ClosedAt == nil {
		if t := parseTime(d.ResolvedAt); t != nil {
			r.ClosedAt = t
		}
	}
	if dup := parseDuplicateOf(d.DuplicateOf); dup != "" {
		r.DuplicateOf = dup
	}

	type timedLog struct {
		log reportLogJSON
		at  time.Time
	}
	var sorted []timedLog
	for _, l := range logs {
		if l.Private {
			continue
		}
		if at := parseTime(l.CreatedAt); at != nil {
			sorted = append(sorted, timedLog{log: l, at: *at})
		}
	}
	slices.SortStableFunc(sorted, func(a, b timedLog) int {
		return a.at.Compare(b.at)
	})

	r.Detailed = true
	r.PaymentsKnown = true
	r.Comments = 0
	r.Events = nil
	r.Payments = nil

	firstSeverity := true
	lastComment := ""
	prevState := ""
	var lastCloseAt *time.Time

	for _, item := range sorted {
		l := item.log
		at := item.at
		actor := logActor(l)
		switch actor {
		case "program":
			if r.FirstResponseAt == nil || at.Before(*r.FirstResponseAt) {
				t := at
				r.FirstResponseAt = &t
			}
			if r.LastProgramActivityAt == nil || at.After(*r.LastProgramActivityAt) {
				t := at
				r.LastProgramActivityAt = &t
			}
		case "hunter":
			if r.LastHunterActivityAt == nil || at.After(*r.LastHunterActivityAt) {
				t := at
				r.LastHunterActivityAt = &t
			}
		}

		switch strings.ToLower(strings.TrimSpace(l.Type)) {
		case "comment":
			r.Comments++
			kind := platform.EventCommentProgram
			if actor == "hunter" {
				kind, lastComment = platform.EventCommentYou, "you"
			} else {
				lastComment = "program"
			}
			r.Events = append(r.Events, platform.ReportEvent{At: at, Kind: kind})

		case "status-update", "status", "workflow-state-update", "close", "reopen", "ask-verif", "ask_verif":
			from, to := logStates(l)
			if from == "" && prevState != "" {
				from = prevState
			}
			if to == "" {
				continue
			}
			r.Events = append(r.Events, platform.ReportEvent{At: at, Kind: platform.EventState, From: from, To: to})
			prevState = to
			if to == "under_review" || to == "accepted" || to == "ask_verif" {
				if r.TriagedAt == nil || at.Before(*r.TriagedAt) {
					t := at
					r.TriagedAt = &t
				}
			}
			if isClosedWorkflowState(to) {
				t := at
				lastCloseAt = &t
			}

		case "cvss-update", "cvss", "severity-update", "severity":
			fromSev, toSev := logSeverities(l)
			if fromSev == "" && toSev == "" {
				continue
			}
			if firstSeverity {
				firstSeverity = false
				if fromSev != "" {
					r.SubmittedSeverity = fromSev
				}
			}
			if actor != "hunter" {
				r.SeverityChanged = true
			}
			r.Events = append(r.Events, platform.ReportEvent{At: at, Kind: platform.EventSeverity, From: fromSev, To: toSev})

		case "reward", "bounty", "reward-update", "bounty-awarded", "payment", "bonus":
			cents, cur, kind, ok := logReward(l, currency)
			if !ok || cents <= 0 {
				continue
			}
			r.Payments = append(r.Payments, platform.ReportPayment{
				Ref:       strings.TrimSpace(l.ID.String()),
				Kind:      kind,
				Amount:    float64(cents) / 100.0,
				Currency:  cur,
				AwardedAt: at,
			})
			r.Events = append(r.Events, platform.ReportEvent{At: at, Kind: platform.EventBounty, To: kind})
			if r.FirstBountyAt == nil || at.Before(*r.FirstBountyAt) {
				t := at
				r.FirstBountyAt = &t
			}
		}
	}

	if firstSeverity && r.SubmittedSeverity == "" {
		r.SubmittedSeverity = r.Severity
	}
	if r.ClosedAt == nil && !isOpenReportState(r.State) && lastCloseAt != nil {
		r.ClosedAt = lastCloseAt
	}
	if len(r.Payments) == 0 && rewardCents > 0 {
		at := r.ClosedAt
		if at == nil {
			at = r.LastActivityAt
		}
		if at == nil {
			t := r.SubmittedAt
			at = &t
		}
		r.Payments = append(r.Payments, platform.ReportPayment{
			Ref:       "total",
			Kind:      "bounty",
			Amount:    float64(rewardCents) / 100.0,
			Currency:  currency,
			AwardedAt: *at,
		})
		if r.FirstBountyAt == nil {
			r.FirstBountyAt = at
		}
	}
	if !isOpenReportState(r.State) {
		r.WaitingOn = ""
	} else if r.WaitingOn == "" && lastComment == "you" {
		r.WaitingOn = "program"
	}
	slices.SortStableFunc(r.Events, func(a, b platform.ReportEvent) int {
		return a.At.Compare(b.At)
	})
}

func logActor(l reportLogJSON) string {
	candidates := []string{l.AuthorType, l.UserType}
	for _, a := range []*logActorJSON{l.Author, l.User} {
		if a != nil {
			candidates = append(candidates, a.Role, a.Type)
		}
	}
	for _, c := range candidates {
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "hunter", "reporter", "researcher":
			return "hunter"
		case "program", "business_unit", "company", "client", "yeswehack", "triage", "triager", "admin", "staff", "moderator", "owner", "manager":
			return "program"
		}
	}
	if l.IsHunter != nil {
		if *l.IsHunter {
			return "hunter"
		}
		return "program"
	}
	return ""
}

func logStates(l reportLogJSON) (from, to string) {
	from = firstNonEmpty(l.OldWorkflowState, statusWord(l.OldStatus), statusWord(l.From))
	to = firstNonEmpty(l.NewWorkflowState, statusWord(l.NewStatus), statusWord(l.Status), statusWord(l.To))
	if to == "" {
		switch strings.ToLower(strings.TrimSpace(l.Type)) {
		case "ask-verif", "ask_verif":
			to = "ask_verif"
		}
	}
	return from, to
}

func statusWord(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var obj reportStatusJSON
	if json.Unmarshal(raw, &obj) == nil {
		return strings.TrimSpace(obj.WorkflowState)
	}
	return ""
}

func logSeverities(l reportLogJSON) (from, to string) {
	if l.OldCVSS != nil {
		from = ywhSeverity(l.OldCVSS.Criticity)
	}
	if from == "" {
		from = cvssWord(l.From)
	}
	if l.NewCVSS != nil {
		to = ywhSeverity(l.NewCVSS.Criticity)
	}
	if to == "" && l.CVSS != nil {
		to = ywhSeverity(l.CVSS.Criticity)
	}
	if to == "" {
		to = cvssWord(l.To)
	}
	return from, to
}

func cvssWord(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return ywhSeverity(s)
	}
	var obj reportCVSSJSON
	if json.Unmarshal(raw, &obj) == nil {
		return ywhSeverity(obj.Criticity)
	}
	return ""
}

func logReward(l reportLogJSON, defaultCurrency string) (cents int64, cur, kind string, ok bool) {
	cur = strings.ToUpper(strings.TrimSpace(l.Currency))
	rewardType := firstNonEmpty(l.RewardType, l.BountyType, l.Type)

	if len(l.Reward) > 0 && string(l.Reward) != "null" {
		var n int64
		if json.Unmarshal(l.Reward, &n) == nil {
			cents = n
		} else {
			var obj struct {
				Amount   *int64 `json:"amount"`
				Reward   *int64 `json:"reward"`
				Currency string `json:"currency"`
				Type     string `json:"type"`
			}
			if json.Unmarshal(l.Reward, &obj) == nil {
				if obj.Amount != nil {
					cents = *obj.Amount
				} else if obj.Reward != nil {
					cents = *obj.Reward
				}
				if c := strings.ToUpper(strings.TrimSpace(obj.Currency)); c != "" {
					cur = c
				}
				if obj.Type != "" {
					rewardType = obj.Type
				}
			}
		}
	}
	if cents <= 0 && l.Amount != nil {
		cents = *l.Amount
	}
	if cents <= 0 {
		return 0, "", "", false
	}
	if cur == "" {
		cur = defaultCurrency
	}
	if cur == "" {
		cur = "EUR"
	}
	kind = "bounty"
	rt := strings.ToLower(rewardType)
	switch {
	case strings.Contains(rt, "bonus"):
		kind = "bonus"
	case strings.Contains(rt, "retest"):
		kind = "retest"
	}
	return cents, cur, kind, true
}

func parseDuplicateOf(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return strings.TrimSpace(n.String())
	}
	var obj struct {
		ID      json.Number `json:"id"`
		LocalID string      `json:"local_id"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		if obj.LocalID != "" {
			return strings.TrimSpace(obj.LocalID)
		}
		return strings.TrimSpace(obj.ID.String())
	}
	return ""
}

func parseTime(s *string) *time.Time {
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

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
