package web

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

func TestWorkbenchUX(t *testing.T) {
	t.Run("workbench cards use internal arrow, clickable headings, and semantic status pills", func(t *testing.T) {
		e := newTestEnv(t)
		for _, path := range []string{"/system", "/integrations"} {
			res, body := e.do(e.client(), http.MethodGet, path, nil, nil)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("%s: status %d", path, res.StatusCode)
			}
			if got := strings.Count(body, "<h1"); got != 1 {
				t.Errorf("%s: rendered %d <h1> elements, want 1", path, got)
			}
			if !strings.Contains(body, `<nav class="trail"`) {
				t.Errorf("%s: missing topbar breadcrumb trail", path)
			}
			_, afterGrid, ok := strings.Cut(body, `<section class="workbench-grid"`)
			if !ok {
				t.Fatalf("%s: missing workbench-grid", path)
			}
			grid, _, ok := strings.Cut(afterGrid, `</section>`)
			if !ok {
				t.Fatalf("%s: unclosed workbench-grid", path)
			}
			if strings.Contains(grid, "#i-external") {
				t.Errorf("%s: workbench cards still use #i-external on internal links", path)
			}
			if !strings.Contains(grid, `class="workbench-card-link"`) {
				t.Errorf("%s: workbench card headings are not clickable (.workbench-card-link)", path)
			}
			if !strings.Contains(grid, `class="workbench-status ok"`) {
				t.Errorf("%s: missing semantic .workbench-status.ok pill", path)
			}
		}
	})

	t.Run("integration subpages render single h1, breadcrumb trail, and aligned llm hero", func(t *testing.T) {
		e := newTestEnv(t)
		for _, path := range []string{"/api", "/mcp", "/scan", "/pojia", "/llm"} {
			res, body := e.do(e.client(), http.MethodGet, path, nil, nil)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("%s: status %d", path, res.StatusCode)
			}
			if got := strings.Count(body, "<h1"); got != 1 {
				t.Errorf("%s: rendered %d <h1> elements, want 1", path, got)
			}
			tbStart := strings.Index(body, `<div class="tb-title">`)
			omniStart := strings.Index(body, `<form class="omni"`)
			if tbStart < 0 || omniStart <= tbStart {
				t.Fatalf("%s: missing topbar structure", path)
			}
			topbar := body[tbStart:omniStart]
			if !strings.Contains(topbar, `<nav class="trail"`) || !strings.Contains(topbar, `href="/integrations"`) {
				t.Errorf("%s: topbar missing /integrations trail: %s", path, topbar)
			}
			if path == "/llm" {
				_, afterHero, ok := strings.Cut(body, `<header class="llm-hero">`)
				if !ok {
					t.Fatalf("/llm: missing .llm-hero")
				}
				hero, _, ok := strings.Cut(afterHero, `</header>`)
				if !ok {
					t.Fatalf("/llm: unclosed .llm-hero")
				}
				if !strings.Contains(hero, `integration-back`) {
					t.Errorf("/llm: .integration-back button is outside .llm-hero")
				}
			}
		}
	})

	t.Run("27-mcp.css does not truncate llm-hero description", func(t *testing.T) {
		cssBytes, err := os.ReadFile("static/css/27-mcp.css")
		if err != nil {
			t.Fatal(err)
		}
		rules := parseCSS(string(cssBytes), "27-mcp.css")
		found := false
		for _, r := range rules {
			if r.sel != ".llm-hero p" {
				continue
			}
			found = true
			if r.props["text-overflow"] == "ellipsis" || r.props["white-space"] == "nowrap" {
				t.Errorf(".llm-hero p still truncates with %v", r.props)
			}
			if r.media == "" && r.props["max-width"] != "72ch" {
				t.Errorf(".llm-hero p max-width = %q, want 72ch", r.props["max-width"])
			}
		}
		if !found {
			t.Fatal("missing .llm-hero p rule in 27-mcp.css")
		}
	})

	t.Run("scan recent table uses table-wrap and responsive cards", func(t *testing.T) {
		e := newTestEnv(t)
		ctx := t.Context()
		if err := e.store.MarkHandedOff(ctx, []store.ScanCandidate{{Target: "a.example.com", Platform: "bugcrowd", Handle: "opensea"}}, "task-ux-1"); err != nil {
			t.Fatal(err)
		}
		if err := e.store.SetHandoffState(ctx, "task-ux-1", "failed", 4, time.Now()); err != nil {
			t.Fatal(err)
		}

		res, body := e.do(e.client(), http.MethodGet, "/scan", nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("/scan: status %d", res.StatusCode)
		}
		if !strings.Contains(body, `<div class="table-wrap"><table class="cards scan-recent-table">`) {
			t.Errorf("/scan: recent table is not wrapped in .table-wrap with .cards.scan-recent-table")
		}
		for _, want := range []string{
			`<td class="c-check">`,
			`<td class="c-name mono small">`,
			`<td data-label="Platform">`,
			`<td class="c-wide small">`,
			`<td class="c-side">`,
			`data-label="When"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("/scan: missing responsive card cell marker %q", want)
			}
		}
	})
}
