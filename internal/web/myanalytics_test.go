package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The range picker's choices and custom dates/months turn into the right spans.
func TestAnalyticsRange(t *testing.T) {
	zone := time.UTC
	now := time.Date(2026, 11, 22, 15, 0, 0, 0, zone)
	month := func(y int, m time.Month) time.Time { return time.Date(y, m, 1, 0, 0, 0, 0, zone) }
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, zone) }
	end := now.Add(time.Second)
	for _, tc := range []struct {
		query    string
		key      string
		from, to time.Time
		label    string
	}{
		{"", "all", time.Time{}, end, "all time"},
		{"range=nonsense", "all", time.Time{}, end, "all time"},
		{"range=month", "month", month(2026, 11), end, "November 2026"},
		{"range=3m", "3m", month(2026, 9), end, "the last 3 months"},
		{"range=12m", "12m", month(2025, 12), end, "the last 12 months"},
		{"range=year", "year", month(2026, 1), end, "2026"},
		{"range=lastyear", "lastyear", month(2025, 1), month(2026, 1), "2025"},
		{"from=2026-01&to=2026-03", "custom", month(2026, 1), month(2026, 4), "Jan 2026 – Mar 2026"},
		{"from=2026-05", "custom", month(2026, 5), end, "May 2026 – Nov 2026"},
		{"from=2026-05&to=2026-02", "custom", month(2026, 5), end, "May 2026 – Nov 2026"}, // to before from: to now
		{"from=junk&range=year", "year", month(2026, 1), end, "2026"},
		{"from=2026-11-01&to=2026-11-27", "custom", day(2026, 11, 1), day(2026, 11, 28), "2026-11-01 – 2026-11-27"},
		{"from=2026-11-22&to=2026-11-22", "custom", day(2026, 11, 22), day(2026, 11, 23), "2026-11-22 – 2026-11-22"},
		{"from=2026-11-01", "custom", day(2026, 11, 1), end, "2026-11-01 – 2026-11-22"},
		{"from=2026-11-10&to=2026-11-05", "custom", day(2026, 11, 10), end, "2026-11-10 – 2026-11-22"},
	} {
		q, err := url.ParseQuery(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		rg, key, label := analyticsRange(q, now, zone)
		if key != tc.key || !rg.From.Equal(tc.from) || !rg.To.Equal(tc.to) || label != tc.label {
			t.Errorf("%q: got %s %v–%v %q, want %s %v–%v %q", tc.query, key, rg.From, rg.To, label, tc.key, tc.from, tc.to, tc.label)
		}
	}
}

// The page says there's nothing yet without reports, and with some shows
// the numbers, charts and programs, over any range.
func TestReportsAnalytics(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	res, body := e.do(c, "GET", "/reports/analytics", nil, nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "No reports yet") {
		t.Fatalf("empty: status %d", res.StatusCode)
	}
	if !strings.Contains(body, "daterange.js") {
		t.Error("empty state lacks daterange.js")
	}
	e.addReports(t)
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"/reports/analytics", []string{"$1.5k", "Reports per month", `class="bc-valid"`, `class="bc-open"`, `href="/programs/fake/acme"`,
			"Lowered by triage", `class="on" aria-current="true">All time`, `class="seg rsubnav"`, `class="ibar"`, ">until<", `class="drp"`, "daterange.js"}},
		{"/reports/analytics?range=3m", []string{`aria-current="true">3 months`, "$1.5k"}},
		{"/reports/analytics?from=2020-01&to=2020-02", []string{"Jan 2020 – Feb 2020", "Nothing awarded in this range", `value="2020-01"`, `value="2020-02"`}},
		{"/reports/analytics?from=2020-01-05&to=2020-02-10", []string{"2020-01-05 – 2020-02-10", "Nothing awarded in this range", `value="2020-01-05"`, `value="2020-02-10"`}},
	} {
		res, body := e.do(c, "GET", tc.path, nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d", tc.path, res.StatusCode)
			continue
		}
		if strings.Contains(body, "{{.") || strings.Contains(body, "{{$") {
			t.Errorf("%s leaked raw template syntax", tc.path)
		}
		for _, w := range tc.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s lacks %q", tc.path, w)
			}
		}
	}
}
