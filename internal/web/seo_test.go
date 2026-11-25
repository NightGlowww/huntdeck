package web

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// Only a read-only copy with a public address is offered to search
// engines, and never its API, searches or scope checks.
func TestSearchEngines(t *testing.T) {
	for _, tt := range []struct {
		name      string
		readOnly  bool
		publicURL string
		indexed   bool
	}{
		{"full install", false, "", false},
		{"full install with an address", false, "https://demo.example.com", false},
		{"unpublished copy", true, "", false},
		{"published copy", true, "https://demo.example.com", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestEnv(t, func(d *Deps) { d.ReadOnly, d.PublicURL = tt.readOnly, tt.publicURL })
			res, body := e.do(e.client(), http.MethodGet, "/programs", nil, nil)
			tag := res.Header.Get("X-Robots-Tag")
			_, robots := e.do(e.client(), http.MethodGet, "/robots.txt", nil, nil)
			sm, smBody := e.do(e.client(), http.MethodGet, "/sitemap.xml", nil, nil)
			if !tt.indexed {
				if tag == "" || !strings.Contains(body, `<meta name="robots" content="noindex, nofollow">`) || robots != "User-agent: *\nDisallow: /\n" ||
					!strings.Contains(body, "<title>Programs · HuntDeck</title>") || strings.Contains(body, "application/ld+json") ||
					sm.StatusCode != http.StatusNotFound || strings.Contains(body, `rel="canonical"`) {
					t.Errorf("indexable: tag %q, robots %q, sitemap %d", tag, robots, sm.StatusCode)
				}
				if res, _ := e.do(e.client(), http.MethodGet, "/sitemaps/pages.xml", nil, nil); res.StatusCode != http.StatusNotFound {
					t.Errorf("sitemaps/pages.xml = %d off a published copy", res.StatusCode)
				}
				if llms, _ := e.do(e.client(), http.MethodGet, "/llms.txt", nil, nil); llms.StatusCode != http.StatusNotFound {
					t.Errorf("llms.txt = %d off a published copy", llms.StatusCode)
				}
				if _, prog := e.do(e.client(), http.MethodGet, "/programs/fake/acme", nil, nil); !strings.Contains(prog, "<title>Acme Corp · HuntDeck</title>") {
					t.Error("program page lost its short title")
				}
				return
			}
			if tag != "" || !strings.Contains(body, `<meta name="robots" content="index, follow">`) {
				t.Errorf("programs page not indexable: tag %q", tag)
			}
			for _, want := range []string{`<link rel="canonical" href="https://demo.example.com/programs">`, `<meta name="description" content="Every public bug bounty program`,
				`<title>Public bug bounty programs on HackerOne, Bugcrowd, Intigriti and YesWeHack</title>`,
				`<meta property="og:title" content="Public bug bounty programs on HackerOne, Bugcrowd, Intigriti and YesWeHack">`, `<meta property="og:url" content="https://demo.example.com/programs">`,
				`<meta property="og:image" content="https://demo.example.com/static/og.png">`, `<meta name="twitter:card" content="summary_large_image">`} {
				if !strings.Contains(body, want) {
					t.Errorf("programs page lacks %s", want)
				}
			}
			// A public program's page shows its own card; a private one's the site's.
			if _, prog := e.do(e.client(), http.MethodGet, "/programs/fake/acme", nil, nil); !strings.Contains(prog, `<meta property="og:image" content="https://demo.example.com/programs/fake/acme/og.png">`) ||
				!strings.Contains(prog, `<meta property="og:image:alt" content="Acme Corp on Fake: HuntScore, bounty range, total paid and scope">`) {
				t.Error("program page lacks its own share image")
			}
			for _, want := range []string{"User-agent: *\nContent-Signal: search=yes, ai-input=yes, ai-train=no\nAllow: /\nDisallow: /api/\n",
				"User-agent: GPTBot\n", "User-agent: ClaudeBot\n", "User-agent: Google-Extended\n", "cohere-training-data-crawler\nDisallow: /\n",
				"Disallow: /feedback\nDisallow: /?\nDisallow: /programs?\n", "Disallow: /invites\nDisallow: /*.csv$\n\n# Training AI models\n",
				"Sitemap: https://demo.example.com/sitemap.xml"} {
				if !strings.Contains(robots, want) {
					t.Errorf("robots.txt lacks %q:\n%s", want, robots)
				}
			}
			// AI search and assistants fetching for someone are let in.
			for _, ua := range []string{"OAI-SearchBot", "ChatGPT-User", "Claude-User", "PerplexityBot", "Googlebot"} {
				if strings.Contains(robots, ua) {
					t.Errorf("robots.txt turns away %s", ua)
				}
			}
			llms, llmsBody := e.do(e.client(), http.MethodGet, "/llms.txt", nil, nil)
			if llms.StatusCode != http.StatusOK || !strings.HasPrefix(llmsBody, "# HuntDeck\n\n> ") ||
				!strings.Contains(llmsBody, "- [Programs](https://demo.example.com/programs): Every public bug bounty program") ||
				!strings.Contains(llmsBody, "https://demo.example.com/sitemap.xml") {
				t.Errorf("llms.txt %d:\n%s", llms.StatusCode, llmsBody)
			}
			if api, _ := e.do(e.client(), http.MethodGet, "/api/v1/status", nil, nil); api.Header.Get("X-Robots-Tag") == "" {
				t.Error("the API may be indexed")
			}
			if sm.StatusCode != http.StatusOK || !strings.Contains(smBody, "<sitemapindex xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\">\n  <sitemap>\n") ||
				!strings.Contains(smBody, "<loc>https://demo.example.com/sitemaps/pages.xml</loc>") ||
				!strings.Contains(smBody, "<loc>https://demo.example.com/sitemaps/fake-1.xml</loc>") || strings.Contains(smBody, "/programs/") {
				t.Errorf("sitemap index %d:\n%s", sm.StatusCode, smBody)
			}
			// The browser's XML tree view styles itself inline: sitemaps allow
			// that, sandboxed, while pages keep their policy.
			if csp := sm.Header.Get("Content-Security-Policy"); csp != xmlPolicy {
				t.Errorf("sitemap policy %q", csp)
			}
			if res, pages := e.do(e.client(), http.MethodGet, "/sitemaps/pages.xml", nil, nil); res.StatusCode != http.StatusOK || res.Header.Get("Content-Security-Policy") != xmlPolicy ||
				!strings.Contains(pages, "  <url>\n    <loc>https://demo.example.com/programs</loc>") || strings.Contains(pages, "/programs/fake/") {
				t.Errorf("pages sitemap %d:\n%s", res.StatusCode, pages)
			}
			if res, fake := e.do(e.client(), http.MethodGet, "/sitemaps/fake-1.xml", nil, nil); res.StatusCode != http.StatusOK ||
				!strings.Contains(fake, "<loc>https://demo.example.com/programs/fake/acme</loc>") || strings.Contains(fake, "/programs/fake/evil") {
				t.Errorf("fake sitemap %d: public programs only:\n%s", res.StatusCode, fake)
			}
			for _, p := range []string{"/sitemaps/fake-2.xml", "/sitemaps/pages", "/sitemaps/nope.xml"} {
				if res, _ := e.do(e.client(), http.MethodGet, p, nil, nil); res.StatusCode != http.StatusNotFound {
					t.Errorf("%s = %d, want 404", p, res.StatusCode)
				}
			}
			_, prog := e.do(e.client(), http.MethodGet, "/programs/fake/acme", nil, nil)
			if !strings.Contains(prog, `<meta name="description" content="Acme Corp: Fake bug bounty program. 2 in-scope targets, 1 wildcard, bounties $100 – $15k.`) {
				i := strings.Index(prog, `name="description"`)
				t.Errorf("program description: %s", prog[max(i, 0):min(len(prog), max(i, 0)+200)])
			}
			for _, want := range []string{
				`<title>Acme Corp bug bounty program (Fake): scope, payouts and stats</title>`,
				`<script type="application/ld+json">{"@context":"https://schema.org","@graph":[{"@type":"BreadcrumbList","itemListElement":[` +
					`{"@type":"ListItem","item":"https://demo.example.com/programs","name":"Programs","position":1},` +
					`{"@type":"ListItem","item":"https://demo.example.com/programs/fake/acme","name":"Acme Corp","position":2}]}]}</script>`,
			} {
				if !strings.Contains(prog, want) {
					t.Errorf("program page lacks %s", want)
				}
			}
			if _, home := e.do(e.client(), http.MethodGet, "/", nil, nil); !strings.Contains(home, `{"@type":"WebSite"`) ||
				!strings.Contains(home, `"url":"https://demo.example.com/"`) {
				t.Error("home page lacks the WebSite structured data")
			}
		})
	}
}

// Each platform's programs go in sitemaps of at most sitemapChunk, named
// by platform and number, each dated by its latest change.
func TestProgramSitemaps(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 12, 0, 0, 0, time.UTC) }
	var progs []store.SitemapProgram
	for i := range sitemapChunk + 1 {
		progs = append(progs, store.SitemapProgram{Platform: "bugcrowd", Handle: fmt.Sprintf("b%03d", i), Changed: day(1 + i%20)})
	}
	progs = append(progs, store.SitemapProgram{Platform: "hackerone", Handle: "a b"}, store.SitemapProgram{Platform: "hackerone", Handle: "c", Changed: day(3)})
	parts := programSitemaps("https://x.example", progs)
	var got []string
	for _, p := range parts {
		got = append(got, fmt.Sprintf("%s:%d:%s", p.Name, len(p.URLs), sitemapDate(p.Changed)))
	}
	if want := []string{"bugcrowd-1:200:2026-10-20", "bugcrowd-2:1:2026-10-01", "hackerone-1:2:2026-10-03"}; !slices.Equal(got, want) {
		t.Errorf("parts %q, want %q", got, want)
	}
	if u := parts[2].URLs[0]; u.Loc != "https://x.example/programs/hackerone/a%20b" || u.LastMod != "" {
		t.Errorf("first hackerone url %+v", u)
	}
	if programSitemaps("https://x.example", nil) != nil {
		t.Error("no programs, yet sitemaps")
	}
}

// A description words a bounty range as the page does.
func TestBountyWords(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, tt := range []struct {
		lo, hi *float64
		want   string
	}{{f(100), f(15000), "$100 – $15k"}, {nil, f(5000), "up to $5k"}, {f(500), nil, "from $500"}, {nil, nil, ""}} {
		if got := bountyWords(tt.lo, tt.hi, "USD"); got != tt.want {
			t.Errorf("bountyWords(%v, %v) = %q, want %q", tt.lo, tt.hi, got, tt.want)
		}
	}
}

// A program added by hand is described as one, with its bounty worded as
// the page has it.
func TestOwnProgramDescription(t *testing.T) {
	lo := 500.0
	d := programData{P: &store.ProgramDetail{ProgramRef: store.ProgramRef{Platform: store.Independent, Name: "Meta Bug Bounty"}, OffersBounty: true},
		InScope: make([]store.EntryView, 23)}
	d.Row.BountyMin, d.Row.Currency = &lo, "USD"
	want := "Meta Bug Bounty: Independent bug bounty program. 23 in-scope targets, bounties from $500. Scope and rules, added by hand."
	if got := programDescription(d); got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

// A program's title says what it is once, and promises only what its page
// has.
func TestProgramTitle(t *testing.T) {
	for _, tt := range []struct {
		platform, name string
		bounty         bool
		want           string
	}{
		{"hackerone", "Acme", true, "Acme bug bounty program (HackerOne): scope, payouts and stats"},
		{"hackerone", "Acme", false, "Acme vulnerability disclosure program (HackerOne): scope and stats"},
		{"bugcrowd", "Acme Bug Bounty", true, "Acme Bug Bounty program (Bugcrowd): scope, payouts and stats"},
		{"intigriti", "Acme Bug Bounty Program", true, "Acme Bug Bounty Program (Intigriti): scope, payouts and stats"},
		{"hackerone", "Acme VDP", false, "Acme VDP (HackerOne): scope and stats"},
		{"yeswehack", "YesWeHack", true, "YesWeHack bug bounty program: scope, payouts and stats"},
		{store.Independent, "Meta Bug Bounty", true, "Meta Bug Bounty program (Independent): scope, payouts and rules"},
		{store.Independent, "Initech", false, "Initech vulnerability disclosure program (Independent): scope and rules"},
	} {
		d := programData{P: &store.ProgramDetail{ProgramRef: store.ProgramRef{Platform: tt.platform, Name: tt.name}, OffersBounty: tt.bounty}}
		if got := programTitle(d); got != tt.want {
			t.Errorf("programTitle(%s %q) = %q, want %q", tt.platform, tt.name, got, tt.want)
		}
	}
}
