package web

import (
	"cmp"
	"context"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/reports"
	"github.com/mdpsec/huntdeck/internal/store"
)

// The Reports page (/reports): the user's own reports from every platform
// and those added by hand, with what needs them. Private: never in a
// read-only copy (readOnlyHidden).

// reportGroups are the values ?status= takes. The rail's Status filter
// lists the ones not marked View; those are the built-in views'
// (reportViews).
var reportGroups = []struct {
	Key, Label, Title string
	View              bool
}{
	{"open", "Open", "Not decided yet, or valid and waiting for a fix", false},
	{"you", "Needs you", "The program is waiting on you", true},
	{"stale", "Gone quiet", "Not decided yet, and no word from the program for a while (Settings › Reports sets how long)", true},
	{"unpaid", "Awaiting bounty", "Valid, with nothing paid yet", true},
	{"valid", "Valid", "Accepted, resolved, or valid but not to be fixed", false},
	{"closed", "Closed", "Decided and not open", false},
	{"paid", "Paid", "Earned a bounty, bonus or retest", false},
}

// reportGroupOf is the status value's group, or nil.
func reportGroupOf(key string) *struct {
	Key, Label, Title string
	View              bool
} {
	for i := range reportGroups {
		if reportGroups[i].Key == key {
			return &reportGroups[i]
		}
	}
	return nil
}

func inReportGroup(r store.MyReport, group string, now time.Time, quiet time.Duration) bool {
	switch group {
	case "open":
		return reports.Open(r.State) && r.GoneAt == nil
	case "you":
		return reports.NeedsYou(r, now)
	case "stale":
		return reports.Stale(r, now, quiet)
	case "valid":
		return reports.Valid(r.State)
	case "closed":
		return !reports.Open(r.State)
	case "paid":
		return len(r.Payments) > 0
	case "unpaid":
		return reports.Valid(r.State) && len(r.Payments) == 0 && r.GoneAt == nil
	}
	return true
}

// reportQuery is the Reports list's filters, as its query gives them;
// the page, its CSV and the API read them alike. Platform, state,
// severity and year take several values, comma-separated; status one.
type reportQuery struct {
	group                          string
	platforms, states, sevs, years []string
	search                         string
	program                        int64 // a program page's "All" link
	zone                           *time.Location
	now                            time.Time
	quiet                          time.Duration // gone quiet after
}

// reportQueryFrom reads the filters; status falls back to def ("open" on
// the page, "" (all) in the API), and "all" is every report.
func reportQueryFrom(q url.Values, def string, zone *time.Location, now time.Time, quiet time.Duration) reportQuery {
	f := reportQuery{group: q.Get("status"), platforms: listParam(q, "platform"), states: listParam(q, "state"),
		sevs: listParam(q, "severity"), years: listParam(q, "year"), search: strings.ToLower(strings.TrimSpace(q.Get("q"))),
		zone: zone, now: now, quiet: quiet}
	if f.group == "" {
		f.group = def
	}
	if f.group == "all" {
		f.group = ""
	}
	f.program, _ = strconv.ParseInt(q.Get("program"), 10, 64)
	return f
}

// listParam reads a comma-separated parameter.
func listParam(q url.Values, key string) []string {
	var out []string
	for _, v := range strings.Split(q.Get(key), ",") {
		if v = strings.TrimSpace(v); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// match reports whether rep passes every filter but skip's (a rail
// group counts its choices without its own filter).
func (f reportQuery) match(rep store.MyReport, skip string) bool {
	sev := rep.Severity
	if sev == "" {
		sev = "unrated"
	}
	switch {
	case f.program != 0 && (rep.ProgramID == nil || *rep.ProgramID != f.program):
	case skip != "status" && f.group != "" && !inReportGroup(rep, f.group, f.now, f.quiet):
	case skip != "platform" && len(f.platforms) > 0 && !slices.Contains(f.platforms, rep.Platform):
	case skip != "state" && len(f.states) > 0 && !slices.Contains(f.states, rep.State):
	case skip != "severity" && len(f.sevs) > 0 && !slices.Contains(f.sevs, sev):
	case skip != "year" && len(f.years) > 0 && !slices.Contains(f.years, strconv.Itoa(rep.SubmittedAt.In(f.zone).Year())):
	case f.search != "" && !strings.Contains(strings.ToLower(rep.Title+" "+rep.ProgramName+" "+rep.ProgramHandle), f.search):
	default:
		return true
	}
	return false
}

// multiOption is a choice among several of key's values: it turns itself
// on or off, keeping the others chosen (in order, so a list has one
// address).
func multiOption(lq listQuery, label, title, key, value string, chosen, order []string, count int) railOption {
	on := slices.Contains(chosen, value)
	var next []string
	for _, v := range order {
		if v == value && !on || v != value && slices.Contains(chosen, v) {
			next = append(next, v)
		}
	}
	return railOption{Label: label, Title: title, Count: count, Counted: true, On: on, URL: lq.with(key, strings.Join(next, ","))}
}

// reportRow is one report in the list.
type reportRow struct {
	store.MyReport
	Program    store.ProgramRef // ID 0 when HuntDeck doesn't have the program
	StateLabel string
	StateClass string
	Earned     []reports.Amount
	NeedsYou   bool
	Stale      bool
	Quiet      bool
	Agreement  string
}

type reportsView struct {
	RailView  railView
	Pills     []pill
	ClearURL  string
	Rows      []reportRow
	Pager     pager
	Sort      string
	Reverse   bool
	Query     listQuery
	Total     int  // reports stored, before filters
	QuietDays int  // gone quiet after (Settings › Reports)
	OpenOnly  bool // the list keeps to open reports (the default)
	Open      int
	NeedsYou  int
	Stale     int
	EarnedYTD []reports.Amount
	Year      int
	Synced    bool // any platform's reports were read
}

// SortURL is the list sorted by key (again: turned around).
func (v reportsView) SortURL(key string) string {
	l := v.Query.without("rev")
	if key == v.Sort && !v.Reverse {
		l.query.Set("rev", "1")
	}
	return l.with("sort", key)
}

// SortClass marks the column the list is sorted by.
func (v reportsView) SortClass(key string) string { return sortClass(v.Sort == key, v.Reverse) }

const reportsPerPage = 100

func (s *server) myReports(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	all, err := s.Store.MyReports(ctx, store.ReportFilter{})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	now := s.Store.Now()
	quietDays, quiet := s.quietAfter(ctx)
	q := r.URL.Query()
	lq := listQuery{path: "/reports", query: q}
	v := reportsView{Query: lq, Total: len(all), QuietDays: quietDays, Year: now.In(s.shownZone()).Year(), Sort: q.Get("sort"), Reverse: q.Get("rev") == "1"}
	if v.Sort == "" {
		v.Sort = "activity"
	}
	yearStart := time.Date(v.Year, 1, 1, 0, 0, 0, 0, s.shownZone())
	v.EarnedYTD = reports.Earned(all, func(p store.MyPayment) bool { return !p.AwardedAt.Before(yearStart) })
	for _, rep := range all {
		if reports.Open(rep.State) && rep.GoneAt == nil {
			v.Open++
		}
		if reports.NeedsYou(rep, now) {
			v.NeedsYou++
		}
		if reports.Stale(rep, now, quiet) {
			v.Stale++
		}
		if rep.Source == store.ReportFromPlatform {
			v.Synced = true
		}
	}

	f := reportQueryFrom(q, "open", s.shownZone(), now, quiet)
	match := f.match
	count := func(skip string, pick func(store.MyReport) bool) int {
		n := 0
		for _, rep := range all {
			if match(rep, skip) && pick(rep) {
				n++
			}
		}
		return n
	}

	var shown []store.MyReport
	for _, rep := range all {
		if match(rep, "") {
			shown = append(shown, rep)
		}
	}
	sortReports(shown, v.Sort, v.Reverse)
	v.Pager = newPager(r, reportsPerPage, len(shown))
	for _, rep := range shown[min(v.Pager.Offset(), len(shown)):min(v.Pager.Offset()+reportsPerPage, len(shown))] {
		v.Rows = append(v.Rows, s.reportRow(rep, now, quiet))
	}
	v.OpenOnly = f.group == "open"

	// The filter rail. Status is one choice, Open by default; the rest
	// take several.
	status := q.Get("status")
	statusOpts := []railOption{lq.fallback("Open", "Not decided yet, or valid and waiting for a fix (the default)", "status",
		status == "" || status == "open", count("status", func(rep store.MyReport) bool { return inReportGroup(rep, "open", now, quiet) }), true)}
	for _, g := range reportGroups {
		if g.View || g.Key == "open" {
			continue
		}
		statusOpts = append(statusOpts, lq.option(g.Label, g.Title, "status", g.Key,
			count("status", func(rep store.MyReport) bool { return inReportGroup(rep, g.Key, now, quiet) }), true))
	}
	statusOpts = append(statusOpts, lq.option("All", "Every report, open or not", "status", "all",
		count("status", func(store.MyReport) bool { return true }), true))

	var stateOpts []railOption
	stateKeys := make([]string, len(reports.States))
	for i, st := range reports.States {
		stateKeys[i] = st.Key
	}
	for _, st := range reports.States {
		if n := count("state", func(rep store.MyReport) bool { return rep.State == st.Key }); n > 0 || slices.Contains(f.states, st.Key) {
			stateOpts = append(stateOpts, multiOption(lq, st.Label, "", "state", st.Key, f.states, stateKeys, n))
		}
	}
	plats := reportPlatforms(all)
	var platOpts []railOption
	for _, p := range plats {
		o := multiOption(lq, platformLabel(p), "", "platform", p, f.platforms, plats,
			count("platform", func(rep store.MyReport) bool { return rep.Platform == p }))
		o.Platform = p
		platOpts = append(platOpts, o)
	}
	sevList := append(reports.Severities[:len(reports.Severities):len(reports.Severities)], struct{ Key, Label string }{"unrated", "Unrated"})
	sevKeys := make([]string, len(sevList))
	for i, sv := range sevList {
		sevKeys[i] = sv.Key
	}
	var sevOpts []railOption
	for _, sv := range sevList {
		key := sv.Key
		n := count("severity", func(rep store.MyReport) bool { return rep.Severity == key || (key == "unrated" && rep.Severity == "") })
		if n > 0 || slices.Contains(f.sevs, key) {
			sevOpts = append(sevOpts, multiOption(lq, sv.Label, "", "severity", key, f.sevs, sevKeys, n))
		}
	}
	var yearKeys []string
	for _, y := range reportYears(all, s.shownZone()) {
		yearKeys = append(yearKeys, strconv.Itoa(y))
	}
	var yearOpts []railOption
	for _, ys := range yearKeys {
		yearOpts = append(yearOpts, multiOption(lq, ys, "Submitted in "+ys, "year", ys, f.years, yearKeys,
			count("year", func(rep store.MyReport) bool { return strconv.Itoa(rep.SubmittedAt.In(s.shownZone()).Year()) == ys })))
	}
	groups := []railGroup{{Title: "Status", Options: statusOpts}, {Title: "Platform", Options: platOpts},
		{Title: "State", Options: stateOpts}, {Title: "Severity", Options: sevOpts}, {Title: "Submitted", Options: yearOpts}}
	v.RailView = railView{Path: "/reports", Search: q.Get("q"), Placeholder: "Title or program", Label: "Search your reports",
		Hidden: hiddenParams(q, "q", "page"), Views: newMyViews("/reports", reportViewKeys, reportColumns.Name),
		Groups: append([]railGroup{reportViewGroup(q)}, groups...)}
	// Each filter on is a pill that drops it (a view's status too).
	if g := reportGroupOf(status); g != nil && g.View {
		v.Pills = append(v.Pills, pill{Label: g.Label, URL: lq.with("status", "")})
	}
	for _, p := range lq.pills(groups) {
		if !p.Search {
			v.Pills = append(v.Pills, p)
		}
	}
	if f.program != 0 {
		label := "program"
		for _, rep := range all {
			if rep.ProgramID != nil && *rep.ProgramID == f.program {
				label = rep.ProgramName
				break
			}
		}
		v.Pills = append(v.Pills, pill{Label: label, URL: lq.with("program", "")})
	}
	if q.Get("q") != "" {
		v.Pills = append(v.Pills, pill{Label: "“" + q.Get("q") + "”", URL: lq.with("q", ""), Search: true})
	}
	v.ClearURL = "/reports"
	if q.Get("q") != "" {
		v.ClearURL += "?" + url.Values{"q": {q.Get("q")}}.Encode()
	}
	s.render(w, r, http.StatusOK, "reports", pageData{Title: "Reports", Nav: "reports", Data: v})
}

func (s *server) reportRow(rep store.MyReport, now time.Time, quiet time.Duration) reportRow {
	row := reportRow{MyReport: rep, StateLabel: reports.Label(rep.State), StateClass: reports.Class(rep.State),
		Earned: reports.ReportEarned(rep), NeedsYou: reports.NeedsYou(rep, now), Stale: reports.Stale(rep, now, quiet),
		Quiet: reports.Quiet(rep, now), Agreement: reports.AgreementOf(rep)}
	if rep.ProgramID != nil {
		row.Program = store.ProgramRef{ID: *rep.ProgramID, Platform: rep.Platform, Handle: rep.ProgramHandle, Name: rep.ProgramName,
			Private: rep.ProgramPrivate}
	}
	if row.ProgramName == "" {
		row.ProgramName = rep.ProgramHandle
	}
	return row
}

// sortReports orders the list by a column, by default the latest activity
// first; ties newest first.
func sortReports(rs []store.MyReport, key string, reverse bool) {
	earned := func(r store.MyReport) float64 {
		t := 0.0
		for _, p := range r.Payments {
			t += p.Amount // mixed currencies only order roughly; they aren't added up anywhere shown
		}
		return t
	}
	last := func(r store.MyReport) time.Time {
		if r.LastActivityAt != nil {
			return *r.LastActivityAt
		}
		return r.SubmittedAt
	}
	by := map[string]func(a, b store.MyReport) int{
		"submitted": func(a, b store.MyReport) int { return b.SubmittedAt.Compare(a.SubmittedAt) },
		"activity":  func(a, b store.MyReport) int { return last(b).Compare(last(a)) },
		"bounty":    func(a, b store.MyReport) int { return cmp.Compare(earned(b), earned(a)) },
		"severity": func(a, b store.MyReport) int {
			return cmp.Compare(reports.SeverityRank(b.Severity), reports.SeverityRank(a.Severity))
		},
		"program": func(a, b store.MyReport) int {
			return strings.Compare(strings.ToLower(a.ProgramName), strings.ToLower(b.ProgramName))
		},
		"state": func(a, b store.MyReport) int { return cmp.Compare(stateOrder(a.State), stateOrder(b.State)) },
	}
	f, ok := by[key]
	if !ok {
		f = by["activity"]
	}
	slices.SortStableFunc(rs, func(a, b store.MyReport) int {
		c := f(a, b)
		if reverse {
			c = -c
		}
		if c == 0 {
			c = b.SubmittedAt.Compare(a.SubmittedAt)
		}
		return c
	})
}

func stateOrder(s string) int {
	for i, st := range reports.States {
		if st.Key == s {
			return i
		}
	}
	return len(reports.States)
}

func reportPlatforms(rs []store.MyReport) []string {
	var out []string
	for _, r := range rs {
		if !slices.Contains(out, r.Platform) {
			out = append(out, r.Platform)
		}
	}
	slices.Sort(out)
	return out
}

func reportYears(rs []store.MyReport, zone *time.Location) []int {
	var out []int
	for _, r := range rs {
		if y := r.SubmittedAt.In(zone).Year(); !slices.Contains(out, y) {
			out = append(out, y)
		}
	}
	slices.Sort(out)
	slices.Reverse(out)
	return out
}

// hiddenParams are the query's parameters except skip, for a form that
// keeps them.
func hiddenParams(q url.Values, skip ...string) []param {
	var out []param
	for _, k := range slices.Sorted(maps.Keys(q)) {
		if slices.Contains(skip, k) {
			continue
		}
		for _, v := range q[k] {
			if v != "" {
				out = append(out, param{k, v})
			}
		}
	}
	return out
}

// quietAfter is how long an undecided report can go without a word from
// the program before it's gone quiet: Settings › Reports, else the default.
func (s *server) quietAfter(ctx context.Context) (int, time.Duration) {
	days, err := s.Store.ReportQuietDays(ctx, reports.DefaultQuietDays)
	if err != nil {
		days = reports.DefaultQuietDays
	}
	return days, time.Duration(days) * 24 * time.Hour
}
