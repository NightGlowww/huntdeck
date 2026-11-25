package web

import (
	"math"
	"net/http"
	"time"

	"github.com/mdpsec/huntdeck/internal/reports"
	"github.com/mdpsec/huntdeck/internal/store"
)

// The user's own reports in the API: /api/v1/reports (the Reports page's
// list) and /api/v1/reports/analytics (the Analytics page's numbers).
// Private like the pages: a read-only copy doesn't serve them
// (readOnlyHidden). Titles and states only: HuntDeck keeps no write-ups
// or comment text.

type apiMyReportListJSON struct {
	Total   int              `json:"total"`
	Reports []apiMyReportRow `json:"reports"`
}

// apiReportProgram is the program a report went to; Known says HuntDeck
// has it (then platform and handle find it under /api/v1/programs).
type apiReportProgram struct {
	Platform string `json:"platform"`
	Handle   string `json:"handle"`
	Name     string `json:"name"`
	Private  bool   `json:"private"`
	Known    bool   `json:"known"`
}

type apiMyReportRow struct {
	ID                int64              `json:"id"`
	Platform          string             `json:"platform"`
	Key               string             `json:"key"` // the platform's own ID
	Program           apiReportProgram   `json:"program"`
	Title             string             `json:"title"`
	URL               string             `json:"url"`
	State             string             `json:"state"`
	RawState          string             `json:"platform_state"`
	Severity          string             `json:"severity"`
	SeverityLabel     string             `json:"severity_label"`
	SubmittedSeverity string             `json:"submitted_severity"`
	Agreement         string             `json:"severity_agreement"` // match, upgraded, downgraded, changed or ""
	CVSS              *float64           `json:"cvss"`
	SubmittedAt       time.Time          `json:"submitted_at"`
	FirstResponseAt   *time.Time         `json:"first_response_at"`
	TriagedAt         *time.Time         `json:"triaged_at"`
	FirstBountyAt     *time.Time         `json:"first_bounty_at"`
	ClosedAt          *time.Time         `json:"closed_at"`
	LastActivityAt    *time.Time         `json:"last_activity_at"`
	WaitingOn         string             `json:"waiting_on"` // you, program or ""
	NeedsYou          bool               `json:"needs_you"`
	Stale             bool               `json:"gone_quiet"`
	Comments          int                `json:"comments"`
	DuplicateOf       string             `json:"duplicate_of"`
	Source            string             `json:"source"` // platform or manual
	GoneAt            *time.Time         `json:"gone_at"`
	SnoozedUntil      *time.Time         `json:"snoozed_until"`
	Muted             bool               `json:"muted"`
	Edited            []string           `json:"edited"` // fields set by hand, which syncs leave alone
	Earned            []apiAmount        `json:"earned"`
	Payments          []apiReportPayment `json:"payments"`
}

type apiAmount struct {
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
}

type apiReportPayment struct {
	Kind      string     `json:"kind"` // bounty, bonus, retest or other
	Amount    float64    `json:"amount"`
	Currency  string     `json:"currency"`
	AwardedAt time.Time  `json:"awarded_at"`
	PaidAt    *time.Time `json:"paid_at"`
}

func amountsJSON(as []reports.Amount) []apiAmount {
	out := []apiAmount{}
	for _, a := range as {
		out = append(out, apiAmount{a.Currency, a.Value})
	}
	return out
}

func myReportJSON(r store.MyReport, now time.Time, quiet time.Duration) apiMyReportRow {
	row := apiMyReportRow{ID: r.ID, Platform: r.Platform, Key: r.Key, Title: r.Title, URL: r.URL, State: r.State, RawState: r.RawState,
		Program:  apiReportProgram{Platform: r.Platform, Handle: r.ProgramHandle, Name: r.ProgramName, Private: r.ProgramPrivate, Known: r.ProgramID != nil},
		Severity: r.Severity, SeverityLabel: r.SeverityLabel, SubmittedSeverity: r.SubmittedSeverity, Agreement: reports.AgreementOf(r),
		CVSS: r.CVSS, SubmittedAt: r.SubmittedAt, FirstResponseAt: r.FirstResponseAt, TriagedAt: r.TriagedAt, FirstBountyAt: r.FirstBountyAt,
		ClosedAt: r.ClosedAt, LastActivityAt: r.LastActivityAt, WaitingOn: r.WaitingOn, NeedsYou: reports.NeedsYou(r, now),
		Stale: reports.Stale(r, now, quiet), Comments: r.Comments, DuplicateOf: r.DuplicateOf, Source: r.Source, GoneAt: r.GoneAt,
		SnoozedUntil: r.SnoozedUntil, Muted: r.Muted, Edited: []string{}, Earned: amountsJSON(reports.ReportEarned(r)),
		Payments: []apiReportPayment{}}
	for _, f := range store.EditableReportFields {
		if r.Edits[f] {
			row.Edited = append(row.Edited, f)
		}
	}
	for _, p := range r.Payments {
		row.Payments = append(row.Payments, apiReportPayment{p.Kind, p.Amount, p.Currency, p.AwardedAt, p.PaidAt})
	}
	return row
}

func (s *server) apiMyReports(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	all, err := s.Store.MyReports(r.Context(), store.ReportFilter{})
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	since, err := sinceParam(q)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if g := q.Get("status"); g != "" && g != "all" && reportGroupOf(g) == nil {
		writeJSONError(w, http.StatusBadRequest, "status must be one of open, you, stale, unpaid, valid, closed, paid or all")
		return
	}
	limit, offset := clampInt(q.Get("limit"), 100, 1, 1000), clampInt(q.Get("offset"), 0, 0, 1<<30)
	now := s.Store.Now()
	_, quiet := s.quietAfter(r.Context())
	f := reportQueryFrom(q, "", s.shownZone(), now, quiet)
	var shown []store.MyReport
	for _, rep := range all {
		if f.match(rep, "") && (since.IsZero() || !rep.SubmittedAt.Before(since)) {
			shown = append(shown, rep)
		}
	}
	sortReports(shown, q.Get("sort"), false)
	out := apiMyReportListJSON{Total: len(shown), Reports: []apiMyReportRow{}}
	for _, rep := range shown[min(offset, len(shown)):min(offset+limit, len(shown))] {
		out.Reports = append(out.Reports, myReportJSON(rep, now, quiet))
	}
	writeJSON(w, http.StatusOK, out)
}

// The analytics' answer. Rates are null when nothing is decided, times
// null when there are none; money is per currency, and the months,
// severities and programs' earnings are in main_currency.
type apiReportsAnalyticsJSON struct {
	Range          apiAnalyticsRange      `json:"range"`
	Reports        int                    `json:"reports"`
	Open           int                    `json:"open"`
	Decided        int                    `json:"decided"`
	Valid          int                    `json:"valid"`
	Duplicates     int                    `json:"duplicates"`
	Informative    int                    `json:"informative"`
	Rejected       int                    `json:"rejected"`
	Acceptance     *float64               `json:"acceptance"`
	DuplicateRate  *float64               `json:"duplicate_rate"`
	Earned         []apiAmount            `json:"earned"`
	MainCurrency   string                 `json:"main_currency"`
	PerWeek        float64                `json:"reports_per_week"`
	PerMonth       float64                `json:"reports_per_month"`
	EarnedPerMonth float64                `json:"earned_per_month"`
	Months         []apiAnalyticsMonth    `json:"months"`
	Severities     []apiAnalyticsSeverity `json:"severities"`
	Agreement      apiAnalyticsAgreement  `json:"severity_agreement"`
	Times          apiReportTimes         `json:"times"`
	Platforms      []apiAnalyticsPlatform `json:"platforms"`
	Programs       []apiAnalyticsProgram  `json:"programs"`
}

type apiAnalyticsRange struct {
	Key   string     `json:"key"` // all, month, 3m, 12m, year, lastyear or custom
	Label string     `json:"label"`
	From  *time.Time `json:"from"` // null for all time
	To    time.Time  `json:"to"`
}

type apiAnalyticsMonth struct {
	Month      string         `json:"month"` // "2026-10"
	Valid      int            `json:"valid"`
	Duplicates int            `json:"duplicates"`
	Rejected   int            `json:"rejected"`
	Open       int            `json:"open"`
	BySeverity map[string]int `json:"by_severity"`
	Earned     float64        `json:"earned"`
}

type apiAnalyticsSeverity struct {
	Severity  string  `json:"severity"`
	Submitted int     `json:"submitted"` // reports submitted as it
	Rated     int     `json:"rated"`     // reports rated it now
	Valid     int     `json:"valid"`
	Earned    float64 `json:"earned"`
	Paid      int     `json:"paid"`
}

type apiAnalyticsAgreement struct {
	Match      int `json:"match"`
	Upgraded   int `json:"upgraded"`
	Downgraded int `json:"downgraded"`
	Changed    int `json:"changed"` // changed, from what the platform doesn't say
	Unknown    int `json:"unknown"`
}

// apiTime is a set of times from submission, in hours.
type apiTime struct {
	Count  int      `json:"count"`
	Mean   *float64 `json:"mean_hours"`
	Median *float64 `json:"median_hours"`
}

type apiReportTimes struct {
	FirstResponse apiTime `json:"first_response"`
	Triage        apiTime `json:"triage"`
	Bounty        apiTime `json:"bounty"`
	Resolution    apiTime `json:"resolution"`
}

// apiPublishedTimes are a program's averages as its platform publishes
// them, in hours; null where it doesn't.
type apiPublishedTimes struct {
	FirstResponse *float64 `json:"first_response_hours"`
	Triage        *float64 `json:"triage_hours"`
	Validation    *float64 `json:"validation_hours"`
	Bounty        *float64 `json:"bounty_hours"`
	Resolution    *float64 `json:"resolution_hours"`
}

type apiAnalyticsPlatform struct {
	Platform      string      `json:"platform"`
	Reports       int         `json:"reports"`
	Decided       int         `json:"decided"`
	Valid         int         `json:"valid"`
	Duplicates    int         `json:"duplicates"`
	Acceptance    *float64    `json:"acceptance"`
	DuplicateRate *float64    `json:"duplicate_rate"`
	Earned        []apiAmount `json:"earned"`
	Triage        apiTime     `json:"triage"`
	Bounty        apiTime     `json:"bounty"`
}

type apiAnalyticsProgram struct {
	Program    apiReportProgram  `json:"program"`
	Reports    int               `json:"reports"`
	Valid      int               `json:"valid"`
	Duplicates int               `json:"duplicates"`
	Earned     []apiAmount       `json:"earned"`
	Yours      apiReportTimes    `json:"your_times"`
	Theirs     apiPublishedTimes `json:"published_times"`
}

func rateJSON(f float64) *float64 {
	if math.IsNaN(f) {
		return nil
	}
	f = math.Round(f*10000) / 10000
	return &f
}

func timeJSON(s reports.Stat) apiTime {
	t := apiTime{Count: s.N}
	if s.N > 0 {
		mean, median := math.Round(s.Mean*10)/10, math.Round(s.Median*10)/10
		t.Mean, t.Median = &mean, &median
	}
	return t
}

func timesJSON(t reports.Times) apiReportTimes {
	return apiReportTimes{timeJSON(t.FirstResponse), timeJSON(t.Triage), timeJSON(t.Bounty), timeJSON(t.Resolution)}
}

func (s *server) apiReportsAnalytics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	all, err := s.Store.MyReports(ctx, store.ReportFilter{})
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	var ids []int64
	for _, rep := range all {
		if rep.ProgramID != nil {
			ids = append(ids, *rep.ProgramID)
		}
	}
	published, err := s.Store.PublishedTimes(ctx, ids)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	zone, now := s.shownZone(), s.Store.Now()
	rg, key, label := analyticsRange(r.URL.Query(), now, zone)
	a := reports.Analyse(all, rg, zone, published, now)
	out := apiReportsAnalyticsJSON{Range: apiAnalyticsRange{Key: key, Label: label, To: rg.To.UTC()},
		Reports: a.Reports, Open: a.Open, Decided: a.Decided, Valid: a.Valid, Duplicates: a.Duplicates, Informative: a.Informative,
		Rejected: a.Rejected, Acceptance: rateJSON(a.Acceptance), DuplicateRate: rateJSON(a.DuplicateRate), Earned: amountsJSON(a.Earned),
		MainCurrency: a.Main, PerWeek: math.Round(a.PerWeek*100) / 100, PerMonth: math.Round(a.PerMonth*100) / 100,
		EarnedPerMonth: math.Round(a.EarnedPerMonth*100) / 100, Times: timesJSON(a.Times),
		Agreement: apiAnalyticsAgreement{a.Agreement.Match, a.Agreement.Upgraded, a.Agreement.Downgraded, a.Agreement.Changed, a.Agreement.Unknown},
		Months:    []apiAnalyticsMonth{}, Severities: []apiAnalyticsSeverity{}, Platforms: []apiAnalyticsPlatform{}, Programs: []apiAnalyticsProgram{}}
	if !rg.From.IsZero() {
		from := rg.From.UTC()
		out.Range.From = &from
	}
	for _, m := range a.Months {
		by := map[string]int{}
		for k, n := range m.BySeverity {
			by[k] = n
		}
		out.Months = append(out.Months, apiAnalyticsMonth{Month: m.Start.Format("2006-01"), Valid: m.Valid, Duplicates: m.Duplicates,
			Rejected: m.Rejected, Open: m.Open, BySeverity: by, Earned: m.Earned})
	}
	for _, sv := range a.Severities {
		out.Severities = append(out.Severities, apiAnalyticsSeverity{sv.Key, sv.Submitted, sv.Final, sv.Valid, sv.Earned, sv.Paid})
	}
	for _, p := range a.Platforms {
		out.Platforms = append(out.Platforms, apiAnalyticsPlatform{p.Platform, p.Reports, p.Decided, p.Valid, p.Duplicates,
			rateJSON(p.Acceptance), rateJSON(p.DuplicateRate), amountsJSON(p.Earned), timeJSON(p.Triage), timeJSON(p.Bounty)})
	}
	for _, p := range a.Programs {
		t := p.Theirs
		out.Programs = append(out.Programs, apiAnalyticsProgram{
			Program: apiReportProgram{Platform: p.Platform, Handle: p.Handle, Name: p.Name, Private: p.Private, Known: p.ProgramID != nil},
			Reports: p.Reports, Valid: p.Valid, Duplicates: p.Duplicates, Earned: amountsJSON(p.Earned), Yours: timesJSON(p.Yours),
			Theirs: apiPublishedTimes{t.FirstResponse, t.Triage, t.Validation, t.Bounty, t.Resolution}})
	}
	writeJSON(w, http.StatusOK, out)
}
