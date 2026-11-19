package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/fake"
	"github.com/mdpsec/huntdeck/internal/syncer"
)

func TestDisclosedReports(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()

	// The Overview lists the latest; the Stats tab (once "Reports", whose
	// links still work) lists them all.
	_, body := e.do(c, http.MethodGet, "/programs/fake/acme", nil, nil)
	for _, want := range []string{`<a href="?tab=stats">Stats</a>`, `Latest disclosed <span class="count">2</span>`, `<a class="link" href="?tab=stats#disclosed">`,
		`Stored XSS via &lt;script&gt;alert(1)&lt;/script&gt; in name</a>`} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	res, body := e.do(c, http.MethodGet, "/programs/fake/acme?tab=reports", nil, nil)
	if !strings.Contains(body, `<a class="on" aria-current="page" href="?tab=stats">Stats</a>`) {
		t.Error("?tab=reports doesn't open the Stats tab")
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("program page: %d", res.StatusCode)
	}
	for _, want := range []string{
		`id="disclosed"`, `Disclosed reports <span class="count">2</span>`,
		`<a class="link" href="https://platform.example/reports/101" target="_blank" rel="noopener noreferrer nofollow" title="Script runs in the admin panel.">Stored XSS via &lt;script&gt;alert(1)&lt;/script&gt; in name</a>`,
		`by hunter-example`, `<span class="sev high">high</span>`, `$1,337`, `1 paid · $1.3k`, "Most common:",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("program page lacks %q", want)
		}
	}
	if strings.Contains(body, "<script>alert(1)</script> in name") {
		t.Error("report title not escaped")
	}

	_, body = e.do(c, http.MethodGet, "/programs?sort=disclosed", nil, nil)
	if !strings.Contains(body, `href="/programs/fake/acme?tab=stats#disclosed">2</a>`) {
		t.Error("programs list lacks the disclosed count link")
	}

	_, body = e.do(c, http.MethodGet, "/api/v1/programs/fake/acme", nil, nil)
	var out struct {
		Disclosed struct {
			Total      int            `json:"total"`
			BySeverity map[string]int `json:"by_severity"`
			Reports    []struct {
				ID, URL, Reporter string
				Bounty            *float64
			} `json:"reports"`
		} `json:"disclosed_reports"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	d := out.Disclosed
	if d.Total != 2 || d.BySeverity["high"] != 1 || len(d.Reports) != 2 || d.Reports[0].ID != "101" ||
		d.Reports[0].Bounty == nil || *d.Reports[0].Bounty != 1337 {
		t.Errorf("API disclosed_reports: %+v", d)
	}
}

func TestDisclosedPage(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	res, body := e.do(c, http.MethodGet, "/disclosed", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	for _, want := range []string{`href="/disclosed" aria-current="page"`, "2 reports",
		`Stored XSS via &lt;script&gt;alert(1)&lt;/script&gt; in name`, `href="/programs/fake/acme?tab=stats#disclosed"`,
		`<a class="fc" href="/disclosed?weakness=Open&#43;Redirect" title="Open Redirect"><span class="box" aria-hidden="true"></span><span class="fl">Open Redirect</span><em>1</em></a>`, `" data-ext>`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// Newest first by default.
	if strings.Index(body, "Stored XSS") > strings.Index(body, "Open redirect") {
		t.Error("not newest first")
	}

	for _, tt := range []struct {
		query string
		want  []string
	}{
		{"", []string{"101", "102"}},
		{"severity=high%2B", []string{"101"}},
		{"severity=low", []string{"102"}},
		{"paid=1", []string{"101"}},
		{"weakness=Open+Redirect", []string{"102"}},
		{"q=redirect", []string{"102"}},
		{"q=acme+corp", []string{"101", "102"}},
		{"sort=bounty", []string{"101", "102"}},
	} {
		_, body := e.do(c, http.MethodGet, "/api/v1/disclosed?"+tt.query, nil, nil)
		var out struct {
			Total   int `json:"total"`
			Reports []struct {
				ID      string `json:"id"`
				Program struct {
					Handle string `json:"handle"`
				} `json:"program"`
			} `json:"reports"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("%s: %v: %s", tt.query, err, body)
		}
		var ids []string
		for _, r := range out.Reports {
			ids = append(ids, r.ID)
			if r.Program.Handle != "acme" {
				t.Errorf("%s: program %q", tt.query, r.Program.Handle)
			}
		}
		if strings.Join(ids, ",") != strings.Join(tt.want, ",") || out.Total != len(tt.want) {
			t.Errorf("%s: ids %v total %d, want %v", tt.query, ids, out.Total, tt.want)
		}
	}
}

func TestAcceptedReports(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	ctx := context.Background()
	now := time.Now().UTC()
	at := func(daysAgo int) time.Time { return now.Add(-time.Duration(daysAgo) * 24 * time.Hour) }
	ptrAt := func(daysAgo int) *time.Time { v := at(daysAgo); return &v }
	e.fake.SetAccepted(at(182),
		platform.AcceptedReport{ID: "a1", ProgramHandle: "acme", SubmittedAt: ptrAt(9), AcceptedAt: at(3), Priority: 1, Bounty: ptrTo(3000.0)},
		platform.AcceptedReport{ID: "a2", ProgramHandle: "acme", SubmittedAt: ptrAt(30), AcceptedAt: at(20), ResolvedAt: ptrAt(10), Priority: 3},
		platform.AcceptedReport{ID: "a3", ProgramHandle: "acme", SubmittedAt: ptrAt(130), AcceptedAt: at(120), Priority: 4},
	)
	if _, err := e.syncer.Poll(ctx, fake.Accepting{Platform: e.fake}, syncer.Options{Mode: syncer.ModeFull, Trigger: "manual"}); err != nil {
		t.Fatal(err)
	}
	_, body := e.do(c, http.MethodGet, "/programs/fake/acme?tab=stats", nil, nil)
	for _, want := range []string{`id="accepted"`, "Accepted reports <span class=\"count\">3</span>", "Accepted · 90 days</dt><dd>2</dd>",
		"▲ &#43;1", "Still open</dt><dd>2</dd>", "20d", "$3,000", " · 1 accepted (P1 1)", `<span class="nod">`, `<path class="line rest"`} {
		if !strings.Contains(body, want) {
			t.Errorf("program page lacks %q", want)
		}
	}
	// Programs whose feed was read but has never listed a report keep it
	// to themselves, and say so.
	_, body = e.do(c, http.MethodGet, "/programs/fake/evil?tab=stats", nil, nil)
	if !strings.Contains(body, "list its reports on CrowdStream") {
		t.Error("evil's page lacks the hidden CrowdStream note")
	}
	// The stats page charts the platform's accepted reports per month, from
	// where CrowdStream's are complete.
	_, body = e.do(c, http.MethodGet, "/stats", nil, nil)
	for _, want := range []string{`aria-label="Valid reports per month"`, `<td data-label="Valid reports · `} {
		if !strings.Contains(body, want) {
			t.Errorf("stats page lacks %q", want)
		}
	}

	_, body = e.do(c, http.MethodGet, "/api/v1/programs/fake/acme", nil, nil)
	var p struct {
		Accepted struct {
			Total   int  `json:"total"`
			Open    int  `json:"open"`
			Prev90d *int `json:"accepted_prev_90d"`
			Months  []struct {
				Known      bool           `json:"known"`
				ByPriority map[string]int `json:"accepted_by_priority"`
			} `json:"months"`
		} `json:"accepted_reports"`
	}
	if err := json.Unmarshal([]byte(body), &p); err != nil || p.Accepted.Total != 3 || p.Accepted.Open != 2 ||
		p.Accepted.Prev90d == nil || *p.Accepted.Prev90d != 1 || len(p.Accepted.Months) != 24 ||
		p.Accepted.Months[0].Known || !p.Accepted.Months[23].Known {
		t.Errorf("api accepted = %+v, %v", p.Accepted, err)
	}
}

func TestResolvedReports(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	ctx := context.Background()
	now := time.Now().UTC()
	at := func(daysAgo int) time.Time { return now.Add(-time.Duration(daysAgo) * 24 * time.Hour) }
	sub := func(daysAgo int) *time.Time { v := at(daysAgo); return &v }
	e.fake.SetResolutions(
		platform.Resolution{ID: "201", ProgramHandle: "acme", SubmittedAt: sub(14), ActivityAt: at(4), Action: platform.ActionResolved},
		platform.Resolution{ID: "202", ProgramHandle: "acme", SubmittedAt: sub(20), ActivityAt: at(2), Action: platform.ActionBounty, Bounty: ptrTo(2500.0)},
		platform.Resolution{ID: "203", ProgramHandle: "acme", SubmittedAt: sub(150), ActivityAt: at(120), Action: platform.ActionResolved},
	)
	if _, err := e.syncer.Poll(ctx, e.fake, syncer.Options{Mode: syncer.ModeFull, Trigger: "manual"}); err != nil {
		t.Fatal(err)
	}
	// The charts are on the Reports tab only; the headline has the count.
	if _, body := e.do(c, http.MethodGet, "/programs/fake/acme", nil, nil); strings.Contains(body, `id="resolved"`) {
		t.Error("the overview repeats the resolved panel")
	}
	_, body := e.do(c, http.MethodGet, "/programs/fake/acme?tab=stats", nil, nil)
	for _, want := range []string{`id="resolved"`, "Resolved reports <span class=\"count\">3</span>", "Resolved · 90 days</dt><dd>2</dd>",
		"▲ &#43;1", "10d", "$2,500", `<div class="mchart">`, "resolved (so far)", "dashed: this month so far", "Submitted per month", "dashed: still filling in",
		"Bounties per month", "$2,500 shown (so far)", "Days to resolve"} {
		if !strings.Contains(body, want) {
			t.Errorf("program page lacks %q", want)
		}
	}
	// The programs list has the 90-day count in its own column.
	_, body = e.do(c, http.MethodGet, "/programs?q=acme", nil, nil)
	if want := `<td class="c-resolved num nowrap charted" data-label="Valid · 90d"><span class="v"><span title="Valid reports in the last 90 days">2</span></span>`; !strings.Contains(body, want) {
		t.Errorf("programs list lacks %q", want)
	}
	// The stats page charts the platform's valid reports, bounties shown
	// and days to resolve per month.
	_, body = e.do(c, http.MethodGet, "/stats", nil, nil)
	for _, want := range []string{`aria-label="Valid reports per month"`, " resolved (so far)", `aria-label="Bounties shown per month"`,
		"$2,500 shown (so far)", `aria-label="Median days to resolve, by the month resolved"`, `data-label="Days to resolve · `} {
		if !strings.Contains(body, want) {
			t.Errorf("stats page lacks %q", want)
		}
	}
	// A program without resolutions but with its history fetched says so.
	_, body = e.do(c, http.MethodGet, "/programs/fake/evil?tab=stats", nil, nil)
	if !strings.Contains(body, "No resolved reports on Hacktivity") {
		t.Error("evil's page lacks the empty resolved panel")
	}

	_, body = e.do(c, http.MethodGet, "/api/v1/programs/fake/acme", nil, nil)
	var p struct {
		Resolved struct {
			Total   int `json:"total"`
			Last90d int `json:"resolved_90d"`
			Months  []struct {
				Month    string `json:"month"`
				Resolved int    `json:"resolved"`
			} `json:"months"`
		} `json:"resolved_reports"`
	}
	if err := json.Unmarshal([]byte(body), &p); err != nil || p.Resolved.Total != 3 || p.Resolved.Last90d != 2 || len(p.Resolved.Months) != 24 {
		t.Errorf("api resolved = %+v, %v", p.Resolved, err)
	}
}
