package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

func (e *testEnv) addReports(t *testing.T) []store.MyReport {
	t.Helper()
	ctx := context.Background()
	now := e.store.Now()
	_, err := e.store.SaveReports(ctx, "fake", []platform.Report{
		{Key: "r1", ProgramHandle: "acme", Title: "Stored <b>XSS</b> on example.com", URL: "https://example.com/r1", State: platform.ReportResolved,
			RawState: "resolved", Severity: platform.SeverityHigh, SubmittedSeverity: platform.SeverityMedium, SeverityChanged: true,
			SubmittedAt: now.Add(-40 * 24 * time.Hour), Detailed: true, PaymentsKnown: true, Marker: "m",
			Payments: []platform.ReportPayment{{Kind: "bounty", Amount: 1500, Currency: "USD", AwardedAt: now.Add(-10 * 24 * time.Hour)}},
			Events:   []platform.ReportEvent{{At: now.Add(-39 * 24 * time.Hour), Kind: platform.EventState, To: "triaged"}}},
		{Key: "r2", ProgramName: "Somewhere Else", Title: "Open redirect", State: platform.ReportNew, WaitingOn: "you",
			SubmittedAt: now.Add(-20 * 24 * time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	rs, err := e.store.MyReports(ctx, store.ReportFilter{})
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// The list shows each report escaped, with what needs the user, and its
// filters narrow it; a report's page shows its timeline and earnings.
func TestReportsPages(t *testing.T) {
	e := newTestEnv(t)
	rs := e.addReports(t)
	c := e.client()
	res, body := e.do(c, "GET", "/reports", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	// Open reports by default, as Programs keeps to open programs.
	if strings.Contains(body, "Stored &lt;b&gt;XSS") || !strings.Contains(body, "1 open report") {
		t.Error("the list doesn't keep to open reports by default")
	}
	// Latest activity first by default.
	if !strings.Contains(body, `class="sort on" href="/reports?rev=1&amp;sort=activity">Activity`) {
		t.Error("the list isn't sorted by latest activity by default")
	}
	_, body = e.do(c, "GET", "/reports?status=all", nil, nil)
	for _, want := range []string{"Stored &lt;b&gt;XSS&lt;/b&gt; on example.com", "Open redirect", "needs you", "Somewhere Else",
		`href="/programs/fake/acme"`, "$1.5k", `class="sev medium was"`} {
		if !strings.Contains(body, want) {
			t.Errorf("list lacks %q", want)
		}
	}
	if strings.Contains(body, "<b>XSS</b>") {
		t.Error("a title isn't escaped")
	}
	_, body = e.do(c, "GET", "/reports?status=you", nil, nil)
	if strings.Contains(body, "Stored &lt;b&gt;") || !strings.Contains(body, "Open redirect") {
		t.Error("Needs you shows the wrong reports")
	}
	_, body = e.do(c, "GET", "/reports?status=paid&q=xss", nil, nil)
	if !strings.Contains(body, "Stored &lt;b&gt;") || strings.Contains(body, "Open redirect") {
		t.Error("paid + search shows the wrong reports")
	}
	var paid store.MyReport
	for _, r := range rs {
		if r.Key == "r1" {
			paid = r
		}
	}
	res, body = e.do(c, "GET", "/reports/"+strconv.FormatInt(paid.ID, 10), nil, nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "State: triaged") || !strings.Contains(body, "$1.5k") {
		t.Errorf("report page: %d", res.StatusCode)
	}
	if res, _ := e.do(c, "GET", "/reports/999999", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("missing report: %d", res.StatusCode)
	}
}

// Snooze, mute, edit (kept over syncs), and a report added by hand.
func TestReportActions(t *testing.T) {
	e := newTestEnv(t)
	rs := e.addReports(t)
	c := e.client()
	var open store.MyReport
	for _, r := range rs {
		if r.Key == "r2" {
			open = r
		}
	}
	id := strconv.FormatInt(open.ID, 10)
	h := http.Header{"Origin": {e.srv.URL}}
	if res, _ := e.do(c, "POST", "/reports/"+id+"/snooze", url.Values{"days": {"7"}}, h); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("snooze: %d", res.StatusCode)
	}
	if r, _ := e.store.MyReportByID(context.Background(), open.ID); r.SnoozedUntil == nil {
		t.Error("not snoozed")
	}
	if _, body := e.do(c, "GET", "/reports?status=you", nil, nil); strings.Contains(body, "Open redirect") {
		t.Error("a snoozed report still needs you")
	}
	e.do(c, "POST", "/reports/"+id+"/mute", url.Values{"muted": {"1"}}, h)
	if r, _ := e.store.MyReportByID(context.Background(), open.ID); !r.Muted {
		t.Error("not muted")
	}

	form := url.Values{"title": {"Open redirect on login"}, "program": {"Somewhere Else"}, "url": {""}, "state": {"duplicate"},
		"severity": {""}, "submitted_severity": {"low"}, "submitted": {open.SubmittedAt.In(time.UTC).Format(time.DateOnly)}}
	if res, _ := e.do(c, "POST", "/reports/"+id+"/edit", form, h); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("edit: %d", res.StatusCode)
	}
	r, _ := e.store.MyReportByID(context.Background(), open.ID)
	if r.Title != "Open redirect on login" || r.State != platform.ReportDuplicate || !r.Edits["title"] || r.Edits["program_name"] {
		t.Errorf("after edit: %+v", r)
	}
	bad := url.Values{"title": {"x"}, "state": {"nope"}, "submitted": {"2026-01-01"}}
	if res, body := e.do(c, "POST", "/reports/"+id+"/edit", bad, h); res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Choose a state") {
		t.Errorf("bad edit: %d", res.StatusCode)
	}

	add := url.Values{"platform": {"independent"}, "program": {"Example Corp"}, "title": {"SQLi"}, "state": {"resolved"},
		"severity": {"critical"}, "submitted_severity": {"critical"}, "submitted": {"2026-02-01"}, "closed": {"2026-02-10"},
		"pay_kind_0": {"bounty"}, "pay_amount_0": {"2,000"}, "pay_currency_0": {"eur"}, "pay_date_0": {"2026-02-12"}}
	res, _ := e.do(c, "POST", "/reports/new", add, h)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("add: %d", res.StatusCode)
	}
	all, _ := e.store.MyReports(context.Background(), store.ReportFilter{Platform: "independent"})
	if len(all) != 1 || all[0].Source != store.ReportByHand || len(all[0].Payments) != 1 || all[0].Payments[0].Amount != 2000 ||
		all[0].Payments[0].Currency != "EUR" || all[0].ClosedAt == nil {
		t.Fatalf("added: %+v", all)
	}
	if res, _ := e.do(c, "POST", "/reports/"+strconv.FormatInt(all[0].ID, 10)+"/delete", url.Values{}, h); res.StatusCode != http.StatusSeeOther {
		t.Errorf("delete: %d", res.StatusCode)
	}
	if res, _ := e.do(c, "POST", "/reports/"+id+"/delete", url.Values{}, h); res.StatusCode != http.StatusNotFound {
		t.Errorf("deleting a synced report: %d", res.StatusCode)
	}
}

// A read-only copy never serves the user's reports: Reports and Analytics
// are a tour (a picture with made-up reports), everything else a 404.
func TestReportsHiddenReadOnly(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) { d.ReadOnly = true })
	c := e.client()
	for p, shot := range map[string]string{"/reports": "tour/reports.webp", "/reports/analytics": "tour/analytics.webp"} {
		res, body := e.do(c, "GET", p, nil, nil)
		if res.StatusCode != http.StatusOK || !strings.Contains(body, shot) || !strings.Contains(body, "made-up") || strings.Contains(body, "<table") {
			t.Errorf("%s in a read-only copy: %d, not the tour", p, res.StatusCode)
		}
	}
	for _, p := range []string{"/reports/1", "/reports/new", "/reports.csv", "/api/v1/reports", "/api/v1/reports/analytics"} {
		if res, _ := e.do(c, "GET", p, nil, nil); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d in a read-only copy", p, res.StatusCode)
		}
	}
	if res, _ := e.do(c, "POST", "/reports", url.Values{}, nil); res.StatusCode == http.StatusOK {
		t.Error("a read-only copy takes a POST to Reports")
	}
	if _, body := e.do(c, "GET", "/programs", nil, nil); !strings.Contains(body, `href="/reports"`) {
		t.Error("a read-only copy's sidebar has no Reports tour")
	}
}

// A program's Overview shows the user's reports to it, and links to them
// in Reports, which narrows to the program; a read-only copy shows none.
func TestProgramYourReports(t *testing.T) {
	e := newTestEnv(t)
	rs := e.addReports(t)
	var prog, other int64
	for _, r := range rs {
		if r.ProgramID != nil {
			prog = *r.ProgramID
		} else {
			other = r.ID
		}
	}
	c := e.client()
	_, body := e.do(c, "GET", "/programs/fake/acme", nil, nil)
	all := "/reports?program=" + strconv.FormatInt(prog, 10) + "&status=all"
	for _, want := range []string{`class="panel pmine"`, "Stored &lt;b&gt;XSS&lt;/b&gt; on example.com", "$1.5k",
		`href="` + strings.ReplaceAll(all, "&", "&amp;") + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("program page lacks %q", want)
		}
	}
	if strings.Contains(body, `href="/reports/`+strconv.FormatInt(other, 10)+`"`) {
		t.Error("program page lists another program's report")
	}
	if _, body := e.do(c, "GET", "/programs/fake/acme?tab=scope", nil, nil); strings.Contains(body, "pmine") {
		t.Error("Your reports shows beyond the Overview")
	}
	_, body = e.do(c, "GET", all, nil, nil)
	if !strings.Contains(body, "Stored &lt;b&gt;") || strings.Contains(body, "Open redirect") || !strings.Contains(body, "1 report") {
		t.Error("Reports doesn't narrow to the program")
	}

	ro := newTestEnv(t, func(d *Deps) { d.ReadOnly = true })
	ro.addReports(t)
	if _, body := ro.do(ro.client(), "GET", "/programs/fake/acme", nil, nil); strings.Contains(body, "pmine") || strings.Contains(body, "Stored &lt;b&gt;") {
		t.Error("a read-only copy shows the user's reports on a program page")
	}
}

// The API lists the user's reports with the page's filters, and their
// numbers; a read-only copy serves neither, nor lists them on the API page.
func TestReportsAPI(t *testing.T) {
	e := newTestEnv(t)
	e.addReports(t)
	c := e.client()
	_, body := e.do(c, "GET", "/api/v1/reports?status=paid", nil, nil)
	var list apiMyReportListJSON
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 || len(list.Reports) != 1 || list.Reports[0].Key != "r1" || !list.Reports[0].Program.Known ||
		len(list.Reports[0].Earned) != 1 || list.Reports[0].Earned[0].Amount != 1500 || list.Reports[0].Agreement != "upgraded" {
		t.Errorf("paid reports: %+v", list)
	}
	_, body = e.do(c, "GET", "/api/v1/reports?status=you", nil, nil)
	if err := json.Unmarshal([]byte(body), &list); err != nil || list.Total != 1 || !list.Reports[0].NeedsYou || list.Reports[0].Program.Known {
		t.Errorf("needs you: %+v", list)
	}
	_, body = e.do(c, "GET", "/api/v1/reports/analytics", nil, nil)
	var a apiReportsAnalyticsJSON
	if err := json.Unmarshal([]byte(body), &a); err != nil {
		t.Fatal(err)
	}
	if a.Reports != 2 || a.Valid != 1 || a.MainCurrency != "USD" || a.Acceptance == nil || *a.Acceptance != 1 || a.Range.Key != "all" || a.Range.From != nil {
		t.Errorf("analytics: %+v", a)
	}

	if res, body := e.do(c, "GET", "/api", nil, nil); res.StatusCode != http.StatusOK || !strings.Contains(body, "/api/v1/reports/analytics") {
		t.Error("the API page doesn't list the reports endpoints")
	}

	ro := newTestEnv(t, func(d *Deps) { d.ReadOnly = true })
	rc := ro.client()
	for _, p := range []string{"/api/v1/reports", "/api/v1/reports/analytics"} {
		if res, _ := ro.do(rc, "GET", p, nil, nil); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d in a read-only copy", p, res.StatusCode)
		}
	}
	if _, body := ro.do(rc, "GET", "/api", nil, nil); strings.Contains(body, "/api/v1/reports") || strings.Contains(body, "Your reports") {
		t.Error("a read-only copy's API page lists the reports endpoints")
	}
}

// Settings › Polling has a switch for reading the user's reports on a
// platform that can, saved with the rest of the platform's row.
func TestReportsSwitch(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) { d.Platforms[0].Reports = func() bool { return true } })
	e.addReports(t)
	c := e.client()
	_, body := e.do(c, "GET", "/settings", nil, nil)
	for _, want := range []string{`name="reports" value="fake"`, `name="reports_enabled_fake" value="1" form="sched" aria-label="Read your fake reports" checked`,
		`href="/reports?platform=fake">2 reports`} {
		if !strings.Contains(body, want) {
			t.Errorf("settings lack %q", want)
		}
	}
	form := url.Values{"platform": {"fake"}, "enabled": {"1"}, "quick": {"30m"}, "full": {"1d"}, "reports": {"fake"}}
	if res, _ := e.do(c, "POST", "/settings/platform", form, nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d", res.StatusCode)
	}
	if on, _, _ := e.store.ReportSync(context.Background(), "fake"); on {
		t.Error("still on after switching it off")
	}
	form.Set("reports_enabled_fake", "1")
	e.do(c, "POST", "/settings/platform", form, nil)
	if on, _, _ := e.store.ReportSync(context.Background(), "fake"); !on {
		t.Error("still off after switching it on")
	}
	form.Set("reports", "nope")
	if res, _ := e.do(c, "POST", "/settings/platform", form, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("a platform without reports: %d", res.StatusCode)
	}
}

// Reports has what Programs has: built-in views and saved views in the
// rail, a column picker, and the list as CSV (private like the page).
func TestReportsViewsAndCSV(t *testing.T) {
	e := newTestEnv(t)
	e.addReports(t)
	c := e.client()
	_, body := e.do(c, "GET", "/reports", nil, nil)
	for _, want := range []string{`data-views="/reports"`, `href="/reports?status=you"`, `href="/reports?sort=bounty&amp;status=paid"`,
		`data-colpick="reports"`, `table class="fixed flist rlist cards" data-cols="reports"`, `href="/reports.csv" data-csv-cols="reports"`} {
		if !strings.Contains(body, want) {
			t.Errorf("Reports lacks %q", want)
		}
	}
	// A view keeps the platform, and turns off by clicking it again.
	_, body = e.do(c, "GET", "/reports?status=unpaid&platform=fake", nil, nil)
	if !strings.Contains(body, `href="/reports?platform=fake" title="Valid, with nothing paid yet" aria-current="page"`) {
		t.Error("the Awaiting bounty view isn't on, or doesn't turn off to the platform alone")
	}

	res, body := e.do(c, "GET", "/reports.csv?status=paid&cols=report,bounty", nil, nil)
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("csv: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 2 || strings.TrimSpace(lines[0]) != "platform,program,program_handle,report_id,title,url,earned" ||
		!strings.Contains(lines[1], "r1,Stored <b>XSS</b> on example.com") || !strings.Contains(lines[1], "1500 USD") {
		t.Errorf("csv:\n%s", body)
	}

	ro := newTestEnv(t, func(d *Deps) { d.ReadOnly = true })
	if res, _ := ro.do(ro.client(), "GET", "/reports.csv", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("a read-only copy serves the reports CSV: %d", res.StatusCode)
	}
}

// Platforms, states, severities and years take several values at once,
// each option turning itself on or off and keeping the others.
func TestReportsSeveralPlatforms(t *testing.T) {
	e := newTestEnv(t)
	e.addReports(t)
	if _, err := e.store.AddManualReport(context.Background(), store.ReportInput{Platform: "independent", Title: "SQLi in search",
		State: platform.ReportNew, SubmittedAt: e.store.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	c := e.client()
	_, body := e.do(c, "GET", "/reports?platform=independent", nil, nil)
	if !strings.Contains(body, "SQLi in search") || strings.Contains(body, "Open redirect") {
		t.Error("one platform shows the wrong reports")
	}
	if !strings.Contains(body, `href="/reports?platform=fake%2Cindependent"`) && !strings.Contains(body, `href="/reports?platform=independent%2Cfake"`) {
		t.Error("the other platform's option doesn't add it to the chosen one")
	}
	_, body = e.do(c, "GET", "/reports?platform=fake,independent", nil, nil)
	if !strings.Contains(body, "SQLi in search") || !strings.Contains(body, "Open redirect") || strings.Contains(body, "Stored &lt;b&gt;") {
		t.Error("two platforms, open by default, show the wrong reports")
	}
	if !strings.Contains(body, `href="/reports?platform=independent"`) || !strings.Contains(body, `href="/reports?platform=fake"`) {
		t.Error("an option on doesn't turn itself off, keeping the other")
	}
	_, body = e.do(c, "GET", "/reports?status=all&severity=high,unrated", nil, nil)
	if !strings.Contains(body, "Stored &lt;b&gt;") || !strings.Contains(body, "SQLi in search") {
		t.Error("two severities show the wrong reports")
	}
}

// Settings › Reports sets when a report has gone quiet (14 days unless
// changed); the list, its filter and the API follow it.
func TestQuietDaysSetting(t *testing.T) {
	e := newTestEnv(t)
	e.addReports(t) // r2: new, submitted 20 days ago, no word from the program
	c := e.client()
	h := http.Header{"Origin": {e.srv.URL}}
	quiet := func() int {
		_, body := e.do(c, "GET", "/api/v1/reports?status=stale", nil, nil)
		var l apiMyReportListJSON
		if err := json.Unmarshal([]byte(body), &l); err != nil {
			t.Fatal(err)
		}
		return l.Total
	}
	if _, body := e.do(c, "GET", "/reports", nil, nil); !strings.Contains(body, "no word in 14 days") || quiet() != 1 {
		t.Error("not gone quiet after the default 14 days")
	}
	if _, body := e.do(c, "GET", "/settings?tab=reports", nil, nil); !strings.Contains(body, `name="quiet_days" value="14"`) {
		t.Error("Settings › Reports doesn't show the default")
	}
	if res, _ := e.do(c, "POST", "/settings/reports", url.Values{"quiet_days": {"30"}}, h); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d", res.StatusCode)
	}
	if _, body := e.do(c, "GET", "/reports", nil, nil); !strings.Contains(body, "no word in 30 days") || quiet() != 0 {
		t.Error("30 days doesn't take")
	}
	for _, bad := range []string{"0", "366", "soon"} {
		if res, _ := e.do(c, "POST", "/settings/reports", url.Values{"quiet_days": {bad}}, h); res.StatusCode != http.StatusBadRequest {
			t.Errorf("%q: %d", bad, res.StatusCode)
		}
	}
	e.do(c, "POST", "/settings/reports", url.Values{"reset": {"1"}}, h)
	if quiet() != 1 {
		t.Error("reset doesn't go back to 14 days")
	}

	ro := newTestEnv(t, func(d *Deps) { d.ReadOnly = true })
	if _, body := ro.do(ro.client(), "GET", "/settings?tab=reports", nil, nil); strings.Contains(body, "quiet_days") || strings.Contains(body, `tab=reports"`) {
		t.Error("a read-only copy shows Settings › Reports")
	}
}

// A report's timeline shows its payments: a bounty the platform's timeline
// has gets its amount, and a retest is a step of its own, in order.
func TestReportTimelinePayments(t *testing.T) {
	day := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, time.UTC) }
	events := []store.MyReportEvent{
		{Kind: platform.EventBounty, To: "2000.00", At: day(5, 5)},
		{Kind: platform.EventState, From: "retesting", To: "resolved", At: day(13, 9)},
	}
	pays := []store.MyPayment{
		{Kind: "bounty", Amount: 2000, Currency: "USD", AwardedAt: day(5, 6)},
		{Kind: "retest", Amount: 150, Currency: "USD", AwardedAt: day(13, 8)},
	}
	var got []string
	for _, e := range reportTimeline(events, pays) {
		got = append(got, e.Label)
	}
	want := []string{"Bounty awarded: $2,000", "Retest paid: $150", "State: retesting → resolved"}
	if !slices.Equal(got, want) {
		t.Errorf("timeline = %q, want %q", got, want)
	}
	// A bounty the timeline doesn't have is added; one it has twice isn't.
	got = got[:0]
	for _, e := range reportTimeline(nil, pays[:1]) {
		got = append(got, e.Label)
	}
	if !slices.Equal(got, []string{"Bounty awarded: $2,000"}) {
		t.Errorf("payment-only timeline = %q", got)
	}
}
