package web

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestHealthz(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	for _, tt := range []struct {
		db       pinger
		wantCode int
		wantBody string
	}{
		{pinger{}, http.StatusOK, `{"status":"ok"}`},
		{pinger{errors.New("disk I/O error at /data/huntdeck.db")}, http.StatusServiceUnavailable, `{"status":"unavailable"}`},
	} {
		h, err := NewHandler(Deps{Log: log, DB: tt.db})
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if rec.Code != tt.wantCode || strings.TrimSpace(rec.Body.String()) != tt.wantBody {
			t.Errorf("healthz = %d %q, want %d %q", rec.Code, rec.Body.String(), tt.wantCode, tt.wantBody)
		}
	}
}

func TestCrossSitePostRejected(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	for _, path := range []string{"/settings/poll", "/api/v1/poll/fake", "/settings/platform", "/settings/platform/reset", "/settings/huntscore"} {
		res, _ := e.do(c, "POST", path, url.Values{"platform": {"fake"}},
			http.Header{"Sec-Fetch-Site": {"cross-site"}, "Origin": {"https://evil.example"}})
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("cross-site POST %s = %d, want 403", path, res.StatusCode)
		}
	}
	if len(e.poller.calls) != 0 {
		t.Errorf("cross-site POST reached the poller: %v", e.poller.calls)
	}
	if e.sched.Overridden("fake") {
		t.Error("cross-site POST changed the schedule")
	}
}

func TestHostGuardBlocksRebinding(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	for host, want := range map[string]int{
		"127.0.0.1:8080":           http.StatusOK,
		"[::1]:8080":               http.StatusOK,
		"100.64.1.2:8080":          http.StatusOK,
		"localhost:8080":           http.StatusOK,
		"huntdeck.localhost":       http.StatusOK,
		"HuntDeck.Tailnet.Example": http.StatusOK,
		"rebind.attacker.example":  http.StatusMisdirectedRequest,
	} {
		res, body := e.do(c, "GET", "/api/v1/status", nil, http.Header{"Host": {host}})
		if res.StatusCode != want {
			t.Errorf("Host %q = %d, want %d", host, res.StatusCode, want)
		}
		if want != http.StatusOK && strings.Contains(body, "programs") {
			t.Errorf("Host %q leaked data", host)
		}
	}
	if hostAllowed("", nil) || hostAllowed(":8080", nil) {
		t.Error("an empty host must not be allowed")
	}
	// Docker's healthcheck must work whatever the Host header says.
	if res, _ := e.do(c, "GET", "/healthz", nil, http.Header{"Host": {"anything.example"}}); res.StatusCode != http.StatusOK {
		t.Errorf("healthz behind guard = %d", res.StatusCode)
	}
}

func TestPagesRenderAndEscape(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	tests := []struct {
		path string
		want []string
	}{
		// Every page's name sits in the top bar; detail pages show the way back.
		{"/", []string{`<div class="tb-title"><h1>Hunt board</h1></div>`, `<span class="l">Open programs</span><span class="v">2</span>`, `<div class="dl add">`, "https://api.acme.example/v2"}},
		{"/programs", []string{"Acme Corp", "2 open programs</span>", "$41,500"}},
		{"/programs?sort=paid90", []string{`class="sort on" href="/programs?rev=1&amp;sort=paid90"`}},
		{"/programs?sort=paid90&rev=1", []string{`class="sort on rev" href="/programs?sort=paid90"`, `class="sort" href="/programs?sort=scope"`}},
		{"/programs?creds=1", []string{"1 program</span>", "Acme Corp", `creds: Test credentials available from the platform">+`}},
		{"/stats", []string{`<section class="panel ov-tiles ov-highlights" aria-label="All platforms">`, `<a class="ov-plat" href="/stats/fake"`, `<span class="ov-plat">All platforms</span>`,
			`<span class="l">Paid programs</span>`, `<span class="l">Free programs</span>`, `<span class="l">New scope</span>`, `<span class="l">Wildcards in scope</span>`, `<span class="ov-n">$42k</span>`, `<span class="ov-n">340</span>`,
			"line from ", `aria-label="Open programs at each month&#39;s end"`, `<div class="ov-legend">`, "Changed under 5%", `aria-label="Open programs paying no bounties at each month&#39;s end"`}},
		{"/stats?sort=top&bounty=1", []string{`aria-label="All platforms"`}}, // old links still open it
		{"/programs?visibility=private", []string{"1 program</span>"}},
		{"/programs?q=acme", []string{"Acme Corp", "1 program</span>"}},
		{"/programs?sort=scope", []string{"Acme Corp", `class="sort on" href="/programs?rev=1&amp;sort=scope"`}},
		{"/programs?sort=updated", []string{"Acme Corp", "2 open programs</span>"}},
		// A program's page: the headline numbers, then one tab at a time.
		{"/programs/fake/acme", []string{`<nav class="trail" aria-label="Back to"><a href="/programs">Programs</a><span class="sep">/</span><a href="/programs?platform=fake&amp;state=all">`,
			`<span class="l">Total paid</span><span class="v"><span title="$1,250,000 paid in bounties all time">$1.3M</span>`, "$100 – $15,000", "avg $500 – $2k", "first response · ",
			`<a class="on" aria-current="page" href="?">Overview</a>`, `<a href="?tab=scope">Scope <span class="count">2</span></a>`,
			"Why it scores", "$122", "$5,000 – $15,000", "5h", "2d 18h", "44d",
			"$100 – $400", "avg $521 · 43% of reports", "6% of reports", `id="by-asset"`,
			"Before you test", "<b>Test credentials for 1 asset</b><span>*.acme.example</span>", "Within a month",
			`<a class="link" href="https://www.acme.example/about" target="_blank" rel="noopener noreferrer nofollow">acme.example</a>`,
			"Recent changes", `<span class="tag add">`, `data-copy="#copy-wildcards"`, "1 wildcard", `<textarea id="copy-all" hidden readonly aria-hidden="true">*.acme.example`, `<textarea id="copy-out" hidden readonly aria-hidden="true">admin.acme.example`, "1 out of scope</span>"}},
		{"/programs/fake/acme?tab=scope", []string{"*.acme.example", "https://api.acme.example/v2", `*.acme.example <span class="chip creds"`, "All bounty-eligible",
			`<a class="fchip" href="?tab=scope&amp;type=wildcard">Wildcards <em>1</em></a>`, "Before you test", "Sign up with your @wearehackerone.com address"}},
		{"/programs/fake/acme?tab=scope&show=out", []string{"admin.acme.example", `<a class="on" aria-current="true" href="?show=out&amp;tab=scope">Out of scope <em>1</em></a>`}},
		{"/programs/fake/acme?tab=policy", []string{`<a class="t1" href="#policy">Program policy</a>`, `<a class="t2" href="#policy-1">Program rules</a>`,
			"Acme takes <strong>security</strong> seriously.", `<h3 id="policy-1">Program rules</h3>`, "<code>@wearehackerone.com</code>",
			`<a href="https://docs.example.com" target="_blank" rel="noopener noreferrer nofollow">docs</a>`,
			"Reward notes", `<h3 id="rewards-1">Double bounties</h3>`, "On api this month."}},
		{"/programs/fake/acme?tab=changes", []string{`<span class="tag add">`, "https://api.acme.example/v2"}},
		{"/targets?kind=url", []string{"https://api.acme.example/v2"}},
		{"/targets?kind=all&scope=out", []string{`data-copy-url="/api/v1/targets?`, "Copy out-of-scope list</span>"}},
		{"/targets?kind=domain", []string{"app.evil.example", `<div class="tb-title"><h1>Targets</h1></div>`, `<summary class="tb-btn"`, "Download .txt"}},
		{"/targets?kind=domain&scope=out", []string{"admin.acme.example"}},
		{"/changes?since=all", []string{`<span class="tag add">`, "https://api.acme.example/v2"}},
		{"/match?q=https://shop.acme.example/x", []string{"in scope", "*.acme.example"}},
		{"/match?q=admin.acme.example", []string{"out of scope"}},
		{"/match?q=192.0.2.7", []string{"in scope", "192.0.2.0/24"}},
		{"/settings", []string{"Quick poll", `name="quick_fake" value="15m"`, `name="full_fake" value="6h"`, "Save intervals",
			`<a href="/settings" class="on" aria-current="page">Polling`, `<a href="/settings?tab=display">Display</a>`}},
		{"/api", []string{`<nav class="trail" aria-label="Back to"><a href="/integrations">Integrations</a><span class="sep">/</span><span class="here">API</span></nav>`, `<section class="panel ep" id="listPrograms">`, `<span class="mth get">GET</span><code>/api/v1/programs</code>`,
			`/api/v1/programs/fake/acme`, `<form class="try" data-try hidden>`, `href="/api/v1/openapi.json"`, `<a href="/api" aria-current="page">`}},
		{"/settings?tab=display", []string{`<section class="panel" id="theme" data-palettes hidden>`, `<label class="palette" data-palette="midnight">`,
			`<label class="palette" data-palette="nord">`, `<label class="palette" data-palette="graphite">`, `value="graphite" data-palette-pick>`}},
		{"/programs/fake/evil", []string{"&lt;script&gt;alert(1)&lt;/script&gt;", "&lt;img src=x onerror=alert(1)&gt;", `<h2>Platform stats</h2>`}},
		{"/programs/fake/evil?tab=scope", []string{"&lt;img src=x onerror=alert(1)&gt;"}},
	}
	for _, tt := range tests {
		res, body := e.do(c, "GET", tt.path, nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d", tt.path, res.StatusCode)
			continue
		}
		for _, w := range tt.want {
			if !strings.Contains(body, w) {
				t.Errorf("GET %s: missing %q", tt.path, w)
			}
		}
		if strings.Contains(body, "<script>alert") || strings.Contains(body, "<img src=x") {
			t.Errorf("GET %s: unescaped HTML", tt.path)
		}
		if res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("GET %s: missing security headers", tt.path)
		}
	}
	if res, _ := e.do(c, "GET", "/programs/fake/missing", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("missing program = %d", res.StatusCode)
	}
	for _, f := range []string{"css/00-theme.css", "css/15-programs.css", "app.js", "board.js?v=1", "columns.js"} {
		if res, _ := e.do(c, "GET", "/static/"+f, nil, nil); res.StatusCode != http.StatusOK {
			t.Errorf("static %s = %d", f, res.StatusCode)
		}
	}
}
