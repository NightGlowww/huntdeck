package intigriti

// The account's own reports (platform.ReportFetcher), from the web app
// (it needs the web login; the researcher API has no submissions):
//
//   - GET /api/core/researcher/submissions: every report in one list.
//   - GET /api/core/researcher/submissions/{id} for each report whose change
//     marker moved since the last read: its events (state and severity
//     changes, payouts, messages by role), state timeline and payouts.
//   - GET /external/researcher/v1/payouts (the API token), every page: when
//     each payout was paid.
//
// The numbers mean what the app's own code calls them (read from its
// bundle, 2026-11-22): status 1 Triage, 2 Pending, 3 Accepted, 4 Closed,
// 5 Archived; close reason 1 Resolved, 2 Duplicate, 3 Accepted risk,
// 4 Informative, 5 Out of scope, 6 Spam, 7 Not applicable; severity 1 None,
// 2 Low, 3 Medium, 4 High, 5 Critical, 6 Exceptional, 7 Undecided; payout
// status 3 Paid; payout type 1 Bounty, 3 Company bonus.
//
// Never kept: the report, proof of concept, impact, solution, answers, IP
// address, attachments, messages and anyone's name.

import (
	"cmp"
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

// ReportsConfigured implements platform.ReportFetcher: reports need the
// web login.
func (c *Client) ReportsConfigured() bool { return c.loggedIn() }

type submissionState struct {
	Status      int  `json:"status"`
	CloseReason *int `json:"closeReason"`
}

type submissionJSON struct {
	ID                string          `json:"id"`
	ProgramID         string          `json:"programId"`
	ProgramName       string          `json:"programName"`
	Title             string          `json:"title"`
	State             submissionState `json:"state"`
	Severity          int             `json:"severity"`
	SeverityVector    string          `json:"severityVector"`
	HasBonus          bool            `json:"hasBonus"`
	AwaitingFeedback  bool            `json:"awaitingFeedback"`
	HasUnreadMessages bool            `json:"hasUnreadMessages"`
	CreatedAt         int64           `json:"createdAt"`
	LastUpdatedAt     int64           `json:"lastUpdatedAt"`
	DestroyedAt       *int64          `json:"destroyedAt"`
	LastEvent         *struct {
		CreatedAt int64 `json:"createdAt"`
	} `json:"lastEvent"`
}

type submissionEventJSON struct {
	Discriminator int             `json:"discriminator"`
	CreatedAt     int64           `json:"createdAt"`
	IsInternal    bool            `json:"isInternal"`
	From          json.RawMessage `json:"from"`
	To            json.RawMessage `json:"to"`
	Amount        *money          `json:"amount"`
	User          *struct {
		Role string `json:"role"`
	} `json:"user"`
}

type submissionDetailJSON struct {
	submissionJSON
	Events        []submissionEventJSON `json:"events"`
	StateTimeline []struct {
		State     submissionState `json:"state"`
		Timestamp int64           `json:"timestamp"`
	} `json:"stateTimeline"`
	Payouts []struct {
		ID        string `json:"id"`
		Amount    money  `json:"amount"`
		Status    int    `json:"status"`
		Type      int    `json:"type"`
		CreatedAt int64  `json:"createdAt"`
	} `json:"payouts"`
}

// Event discriminators seen on reports.
const (
	eventMessage  = 1
	eventState    = 2
	eventSeverity = 3
)

var (
	statusNames = map[int]string{1: "Triage", 2: "Pending", 3: "Accepted", 4: "Closed", 5: "Archived"}
	closeNames  = map[int]string{1: "Resolved", 2: "Duplicate", 3: "Accepted risk", 4: "Informative", 5: "Out of scope",
		6: "Spam", 7: "Not applicable"}
	closeStates = map[int]string{1: platform.ReportResolved, 2: platform.ReportDuplicate, 3: platform.ReportAcceptedRisk,
		4: platform.ReportInformative, 5: platform.ReportOutOfScope, 6: platform.ReportSpam, 7: platform.ReportNotApplicable}
	severityNames = map[int]string{1: platform.SeverityNone, 2: platform.SeverityLow, 3: platform.SeverityMedium,
		4: platform.SeverityHigh, 5: platform.SeverityCritical, 6: platform.SeverityCritical}
)

// reportState maps a submission's state to HuntDeck's, and words for it.
func reportState(s submissionState) (state, raw string) {
	raw = statusNames[s.Status]
	if raw == "" {
		raw = "Status " + strconv.Itoa(s.Status)
	}
	switch s.Status {
	case 1:
		return platform.ReportNew, raw
	case 2:
		return platform.ReportTriaged, raw
	case 3:
		return platform.ReportAccepted, raw
	case 4, 5:
		if s.CloseReason == nil {
			return platform.ReportNotApplicable, raw
		}
		name := closeNames[*s.CloseReason]
		if name == "" {
			name = "reason " + strconv.Itoa(*s.CloseReason)
		}
		if st, ok := closeStates[*s.CloseReason]; ok {
			return st, raw + ": " + name
		}
		return platform.ReportNotApplicable, raw + ": " + name
	}
	return platform.ReportNew, raw
}

func severityOf(id int) (sev, label string) {
	switch id {
	case 6:
		return platform.SeverityCritical, "Exceptional"
	case 7:
		return "", "Undecided"
	}
	return severityNames[id], ""
}

// unix reads the app's times: seconds (milliseconds, should it switch).
func unix(v int64) *time.Time {
	if v <= 0 {
		return nil
	}
	var t time.Time
	if v > 1e12 {
		t = time.UnixMilli(v).UTC()
	} else {
		t = time.Unix(v, 0).UTC()
	}
	return &t
}

// FetchReports implements platform.ReportFetcher.
func (c *Client) FetchReports(ctx context.Context, known map[string]string, maxDetails int, save platform.ReportsSaver) (bool, error) {
	if !c.loggedIn() {
		return false, ErrNoSession
	}
	var list []submissionJSON
	if err := c.webJSON(ctx, "GET", "/submissions", nil, &list); err != nil {
		return false, fmt.Errorf("intigriti: list reports: %w", err)
	}
	list = slices.DeleteFunc(list, func(s submissionJSON) bool { return s.DestroyedAt != nil && *s.DestroyedAt > 0 })
	slices.SortStableFunc(list, func(a, b submissionJSON) int { return cmp.Compare(b.CreatedAt, a.CreatedAt) })
	var paid map[string]time.Time
	var paidErr error
	var batch []platform.Report
	details := 0
	var detailErr error
	for _, s := range list {
		r := convertSubmission(s)
		if r.Key == "" || r.SubmittedAt.IsZero() {
			continue
		}
		if known[r.Key] != r.Marker && details < maxDetails && detailErr == nil {
			details++
			var d submissionDetailJSON
			if err := c.webJSON(ctx, "GET", "/submissions/"+url.PathEscape(s.ID), nil, &d); err != nil {
				if ctx.Err() != nil {
					return false, err
				}
				detailErr = fmt.Errorf("intigriti: report %s: %w", s.ID, err)
			} else {
				if paid == nil && paidErr == nil && len(d.Payouts) > 0 {
					paid, paidErr = c.payoutDates(ctx)
				}
				addSubmissionDetail(&r, d, paid)
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
	return true, errors.Join(detailErr, paidErr)
}

func convertSubmission(s submissionJSON) platform.Report {
	state, raw := reportState(s.State)
	r := platform.Report{
		Key:           s.ID,
		ProgramHandle: s.ProgramID,
		ProgramName:   s.ProgramName,
		Title:         s.Title,
		URL:           webBase + "/researcher/submissions/" + url.PathEscape(s.ProgramID) + "/" + url.PathEscape(s.ID),
		State:         state,
		RawState:      raw,
		Unread:        s.HasUnreadMessages,
	}
	r.Severity, r.SeverityLabel = severityOf(s.Severity)
	if t := unix(s.CreatedAt); t != nil {
		r.SubmittedAt = *t
	}
	r.LastActivityAt = unix(s.LastUpdatedAt)
	if s.LastEvent != nil {
		if t := unix(s.LastEvent.CreatedAt); t != nil && (r.LastActivityAt == nil || t.After(*r.LastActivityAt)) {
			r.LastActivityAt = t
		}
	}
	if s.AwaitingFeedback && (state == platform.ReportNew || state == platform.ReportTriaged || state == platform.ReportAccepted) {
		r.WaitingOn = "you"
	}
	var lastEvent int64
	if s.LastEvent != nil {
		lastEvent = s.LastEvent.CreatedAt
	}
	closeReason := 0
	if s.State.CloseReason != nil {
		closeReason = *s.State.CloseReason
	}
	r.Marker = fmt.Sprintf("%d|%d|%d|%d|%d|%t", s.State.Status, closeReason, s.Severity, s.LastUpdatedAt, lastEvent, s.HasBonus)
	r.Raw, _ = json.Marshal(map[string]any{"status": s.State.Status, "close_reason": s.State.CloseReason, "severity": s.Severity,
		"severity_vector": s.SeverityVector, "has_bonus": s.HasBonus})
	return r
}

// staff reports whether an event's author speaks for the program
// (Intigriti's triage or the company), not the hunter or a bot.
func staff(e submissionEventJSON) bool {
	return e.User != nil && (e.User.Role == "TRIAGE" || e.User.Role == "COMPANY")
}

func addSubmissionDetail(r *platform.Report, d submissionDetailJSON, paid map[string]time.Time) {
	r.Detailed, r.PaymentsKnown = true, true
	events := slices.Clone(d.Events)
	slices.SortStableFunc(events, func(a, b submissionEventJSON) int { return cmp.Compare(a.CreatedAt, b.CreatedAt) })
	firstSeverity := true
	lastMessage := ""
	r.Comments = 0
	for _, e := range events {
		at := unix(e.CreatedAt)
		if at == nil || e.IsInternal {
			continue
		}
		if staff(e) {
			if r.FirstResponseAt == nil {
				r.FirstResponseAt = at
			}
			r.LastProgramActivityAt = at
		} else if e.User != nil && e.User.Role == "RESEARCHER" {
			r.LastHunterActivityAt = at
		}
		switch e.Discriminator {
		case eventMessage:
			r.Comments++
			kind := platform.EventCommentYou
			if staff(e) {
				kind, lastMessage = platform.EventCommentProgram, "program"
			} else {
				lastMessage = "you"
			}
			r.Events = append(r.Events, platform.ReportEvent{At: *at, Kind: kind})
		case eventState:
			var from, to submissionState
			_ = json.Unmarshal(e.From, &from)
			_ = json.Unmarshal(e.To, &to)
			_, fromRaw := reportState(from)
			_, toRaw := reportState(to)
			if len(e.From) == 0 || string(e.From) == "null" {
				fromRaw = ""
			}
			r.Events = append(r.Events, platform.ReportEvent{At: *at, Kind: platform.EventState, From: fromRaw, To: toRaw})
		case eventSeverity:
			var from, to int
			_ = json.Unmarshal(e.From, &from)
			_ = json.Unmarshal(e.To, &to)
			fromS, _ := severityOf(from)
			toS, _ := severityOf(to)
			if firstSeverity {
				firstSeverity = false
				r.SubmittedSeverity = fromS
			}
			if staff(e) {
				r.SeverityChanged = true
			}
			r.Events = append(r.Events, platform.ReportEvent{At: *at, Kind: platform.EventSeverity, From: fromS, To: toS})
		}
	}
	if firstSeverity {
		r.SubmittedSeverity = r.Severity // never changed: submitted as it is
	}
	if r.WaitingOn == "" && lastMessage == "you" && (r.State == platform.ReportNew || r.State == platform.ReportTriaged || r.State == platform.ReportAccepted) {
		r.WaitingOn = "program"
	}
	for _, t := range d.StateTimeline {
		at := unix(t.Timestamp)
		if at == nil {
			continue
		}
		if (t.State.Status == 2 || t.State.Status == 3) && (r.TriagedAt == nil || at.Before(*r.TriagedAt)) {
			r.TriagedAt = at
		}
		if (t.State.Status == 4 || t.State.Status == 5) && (r.ClosedAt == nil || at.Before(*r.ClosedAt)) {
			r.ClosedAt = at
		}
	}
	for _, p := range d.Payouts {
		at := unix(p.CreatedAt)
		if at == nil || p.Amount.Value <= 0 || p.Amount.Currency == "" {
			continue
		}
		kind := map[int]string{1: "bounty", 3: "bonus"}[p.Type]
		if kind == "" {
			kind = "other"
		}
		status := ""
		var paidAt *time.Time
		if p.Status == 3 {
			status = "paid"
			if t, ok := paid[p.ID]; ok {
				paidAt = &t
			}
		}
		r.Payments = append(r.Payments, platform.ReportPayment{Ref: p.ID, Kind: kind, Amount: p.Amount.Value,
			Currency: strings.ToUpper(p.Amount.Currency), AwardedAt: *at, PaidAt: paidAt, Status: status})
		r.Events = append(r.Events, platform.ReportEvent{At: *at, Kind: platform.EventBounty, To: kind})
		if r.FirstBountyAt == nil || at.Before(*r.FirstBountyAt) {
			r.FirstBountyAt = at
		}
	}
	slices.SortStableFunc(r.Events, func(a, b platform.ReportEvent) int { return a.At.Compare(b.At) })
}

// payoutDates reads when each payout was paid, by payout ID, from the
// researcher API.
func (c *Client) payoutDates(ctx context.Context) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	const limit = 500
	for offset, page := 0, 1; page <= maxPages; offset, page = offset+limit, page+1 {
		var p struct {
			MaxCount int `json:"maxCount"`
			Records  []struct {
				ID     string `json:"id"`
				PaidAt *int64 `json:"paidAt"`
			} `json:"records"`
		}
		u := fmt.Sprintf("%s/v1/payouts?limit=%d&offset=%d", c.base, limit, offset)
		if err := c.getJSON(ctx, u, &p); err != nil {
			return out, fmt.Errorf("intigriti: payouts page %d: %w", page, err)
		}
		for _, r := range p.Records {
			if r.PaidAt != nil {
				if t := unix(*r.PaidAt); t != nil {
					out[r.ID] = *t
				}
			}
		}
		if len(p.Records) < limit || offset+limit >= p.MaxCount {
			return out, nil
		}
	}
	return out, nil
}
