package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The range picker's choices and custom months turn into the right spans.
func TestAnalyticsRange(t *testing.T) {
	zone := time.UTC
	now := time.Date(2026, 11, 22, 15, 0, 0, 0, zone)
	month := func(y int, m time.Month) time.Time { return time.Date(y, m, 1, 0, 0, 0, 0, zone) }
	end := now.Add(time.Second)
	for _, tc := range []struct {
		query    string
		key      string
		from, to time.Time
	}{
		{"", "all", time.Time{}, end},
		{"range=nonsense", "all", time.Time{}, end},
		{"range=month", "month", month(2026, 11), end},
		{"range=3m", "3m", month(2026, 9), end},
		{"range=12m", "12m", month(2025, 12), end},
		{"range=year", "year", month(2026, 1), end},
		{"range=lastyear", "lastyear", month(2025, 1), month(2026, 1)},
		{"from=2026-01&to=2026-03", "custom", month(2026, 1), month(2026, 4)},
		{"from=2026-05", "custom", month(2026, 5), end},
		{"from=2026-05&to=2026-02", "custom", month(2026, 5), end}, // to before from: to now
		{"from=junk&range=year", "year", month(2026, 1), end},
	} {
		q, err := url.ParseQuery(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		rg, key, label := analyticsRange(q, now, zone)
		if key != tc.key || !rg.From.Equal(tc.from) || !rg.To.Equal(tc.to) || label == "" {
			t.Errorf("%q: got %s %v–%v %q, want %s %v–%v", tc.query, key, rg.From, rg.To, label, tc.key, tc.from, tc.to)
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
	e.addReports(t)
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"/reports/analytics", []string{"$1.5k", "Reports per month", `class="bc-valid"`, `class="bc-open"`, `href="/programs/fake/acme"`,
			"Lowered by triage", `class="on" aria-current="true">All time`}},
		{"/reports/analytics?range=3m", []string{`aria-current="true">3 months`, "$1.5k"}},
		{"/reports/analytics?from=2020-01&to=2020-02", []string{"Jan 2020 – Feb 2020", "Nothing awarded in this range"}},
	} {
		res, body := e.do(c, "GET", tc.path, nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d", tc.path, res.StatusCode)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s lacks %q", tc.path, w)
			}
		}
	}
}
