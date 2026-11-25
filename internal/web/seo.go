package web

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// A read-only copy published at HUNTDECK_PUBLIC_URL (Deps.PublicURL) may
// be found through search engines: robots.txt lets crawlers in, except to
// the API, searches and scope checks; /sitemap.xml indexes sitemaps of its
// pages and every public program's; pages carry a description, a canonical link and
// link-preview tags; /llms.txt sums it up for AI assistants. AI search and
// assistants may read it, crawlers gathering pages to train AI models may
// not (robots.txt names them, and says so in a Content-Signal). Any other
// install, a full one above all, asks every crawler to stay out (noindex
// everywhere, robots.txt disallowing all, no llms.txt).

// indexable reports whether search engines may index this copy.
func (s *server) indexable() bool { return s.ReadOnly && s.PublicURL != "" }

// noindexPath reports whether a published copy still keeps a path out of
// search results: the API's answers, searches and scope checks.
func noindexPath(p string) bool {
	return strings.HasPrefix(p, "/api/") || p == "/search" || p == "/match" || p == "/feedback"
}

// aiTrainingCrawlers are the crawlers that gather pages to train AI
// models, which a published copy turns away; AI search and assistants
// fetching a page for someone (OAI-SearchBot, ChatGPT-User, Claude-User,
// PerplexityBot and the like) are let in like search engines.
var aiTrainingCrawlers = []string{"GPTBot", "ClaudeBot", "CCBot", "Google-Extended", "Applebot-Extended", "Bytespider",
	"meta-externalagent", "Amazonbot", "Diffbot", "omgili", "cohere-training-data-crawler"}

// crawlerSkips are what a published copy asks every crawler to leave
// alone besides the API and searches: the lists' filtered and sorted views
// (endless combinations of one list, each a fresh page to build; the plain
// list and every program page are in the sitemaps), CSV downloads, and the
// read-only copy's empty invites tour.
var crawlerSkips = []string{"/?", "/programs?", "/stats?", "/targets?", "/changes?", "/disclosed?", "/invites", "/*.csv$"}

// robots serves robots.txt.
func (s *server) robots(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if !s.indexable() {
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /\n"))
		return
	}
	var b strings.Builder
	b.WriteString("User-agent: *\nContent-Signal: search=yes, ai-input=yes, ai-train=no\nAllow: /\n" +
		"Disallow: /api/\nDisallow: /search\nDisallow: /match\nDisallow: /feedback\n")
	for _, p := range crawlerSkips {
		b.WriteString("Disallow: " + p + "\n")
	}
	b.WriteString("\n# Training AI models\n")
	for _, ua := range aiTrainingCrawlers {
		b.WriteString("User-agent: " + ua + "\n")
	}
	b.WriteString("Disallow: /\n\nSitemap: " + s.PublicURL + "/sitemap.xml\n")
	_, _ = w.Write([]byte(b.String()))
}

// llms serves /llms.txt (llmstxt.org): what a published copy is and its
// main pages, in Markdown, for AI assistants. Not found on other installs.
func (s *server) llms(w http.ResponseWriter, r *http.Request) {
	if !s.indexable() {
		s.renderError(w, r, http.StatusNotFound, "Page not found.")
		return
	}
	u := s.PublicURL
	var b strings.Builder
	b.WriteString("# HuntDeck\n\n> " + siteDescription + "\n\n" +
		"HuntDeck pulls bug bounty programs and their scope from HackerOne, Bugcrowd, Intigriti and YesWeHack, " +
		"keeps a history of every change (scope added or removed, bounties, policy), and collects each program's numbers: " +
		"bounties paid, reports received, response times. HuntScore ranks programs by where a hunter is most likely to be paid " +
		"for new findings. This site is HuntDeck Public: public programs only, refreshed from the platforms regularly. " +
		"HuntDeck itself is free to self-host: run your own copy and it adds your private programs and invites.\n\n## Pages\n\n")
	for _, p := range []struct{ path, name string }{
		{"/", "Hunt board"}, {"/programs", "Programs"}, {"/targets", "Targets"}, {"/changes", "Changes"},
		{"/stats", "Stats"}, {"/disclosed", "Disclosed reports"},
	} {
		nav := strings.TrimPrefix(p.path, "/")
		if nav == "" {
			nav = "dashboard"
		}
		b.WriteString("- [" + p.name + "](" + u + p.path + "): " + pageDescriptions[nav] + "\n")
	}
	b.WriteString("- Program pages, such as " + u + "/programs/hackerone/security: one program's scope, changes, payouts and stats. " +
		"Every one is listed in " + u + "/sitemap.xml.\n\n## Docs\n\n" +
		"- [Methodology](" + u + "/methodology): " + pageDescriptions["methodology"] + "\n" +
		"- [API](" + u + "/api): " + pageDescriptions["api"] + "\n")
	if s.RepoURL != "" {
		b.WriteString("- [Self-host HuntDeck](" + s.RepoURL + "): the source, and how to run your own copy with Docker, with your private programs and invites\n")
	}
	if s.XAccount != "" {
		b.WriteString("\n## Optional\n\n- [@" + s.XAccount + " on X](https://x.com/" + s.XAccount + "): news and launch updates\n")
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

// sitemapPages are a published copy's own pages, besides its programs'.
var sitemapPages = []string{"/", "/programs", "/stats", "/targets", "/changes", "/disclosed", "/methodology", "/api"}

// The sitemap is an index (/sitemap.xml) of small sitemaps: the pages,
// then each platform's programs, sitemapChunk to a file
// (/sitemaps/hackerone-1.xml), so Search Console reports each part apart.
const (
	sitemapChunk = 200
	sitemapNS    = "http://www.sitemaps.org/schemas/sitemap/0.9"
)

// sitemapURL is one page in a sitemap.
type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

// sitemapPart is one sitemap of the index: its name ("pages",
// "hackerone-1"), its pages, and when the latest of them changed.
type sitemapPart struct {
	Name    string
	URLs    []sitemapURL
	Changed time.Time
}

// sitemapDate is a sitemap's lastmod: the day, "" for none.
func sitemapDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

// sitemapParts splits the published pages into the index's sitemaps.
func (s *server) sitemapParts(ctx context.Context) ([]sitemapPart, error) {
	progs, err := s.Store.PublicPrograms(ctx) // by platform, then handle
	if err != nil {
		return nil, err
	}
	pages := sitemapPart{Name: "pages"}
	if at, err := s.Store.LastUpdate(ctx); err != nil {
		return nil, err
	} else if at != nil {
		pages.Changed = *at
	}
	for _, p := range sitemapPages {
		pages.URLs = append(pages.URLs, sitemapURL{Loc: s.PublicURL + p, LastMod: sitemapDate(pages.Changed)})
	}
	return append([]sitemapPart{pages}, programSitemaps(s.PublicURL, progs)...), nil
}

// programSitemaps splits programs, sorted by platform, into each
// platform's sitemaps of at most sitemapChunk: hackerone-1, hackerone-2…
func programSitemaps(base string, progs []store.SitemapProgram) []sitemapPart {
	var parts []sitemapPart
	for i := 0; i < len(progs); {
		platform := progs[i].Platform
		for n := 1; i < len(progs) && progs[i].Platform == platform; n++ {
			part := sitemapPart{Name: platform + "-" + strconv.Itoa(n)}
			for end := min(i+sitemapChunk, len(progs)); i < end && progs[i].Platform == platform; i++ {
				p := progs[i]
				part.URLs = append(part.URLs, sitemapURL{
					Loc:     base + "/programs/" + url.PathEscape(p.Platform) + "/" + url.PathEscape(p.Handle),
					LastMod: sitemapDate(p.Changed),
				})
				if p.Changed.After(part.Changed) {
					part.Changed = p.Changed
				}
			}
			parts = append(parts, part)
		}
	}
	return parts
}

// writeSitemapXML writes v as an indented XML document.
func (s *server) writeSitemapXML(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Security-Policy", xmlPolicy) // the browser's tree view needs its own styles
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(v); err != nil {
		s.Log.Warn("sitemap", "err", err)
		return
	}
	_, _ = w.Write([]byte("\n"))
}

// sitemap serves /sitemap.xml, the index of the sitemaps.
func (s *server) sitemap(w http.ResponseWriter, r *http.Request) {
	if !s.indexable() {
		s.renderError(w, r, http.StatusNotFound, "Page not found.")
		return
	}
	parts, err := s.sitemapParts(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	type entry struct {
		Loc     string `xml:"loc"`
		LastMod string `xml:"lastmod,omitempty"`
	}
	index := struct {
		XMLName  xml.Name `xml:"sitemapindex"`
		NS       string   `xml:"xmlns,attr"`
		Sitemaps []entry  `xml:"sitemap"`
	}{NS: sitemapNS}
	for _, p := range parts {
		index.Sitemaps = append(index.Sitemaps, entry{Loc: s.PublicURL + "/sitemaps/" + p.Name + ".xml", LastMod: sitemapDate(p.Changed)})
	}
	s.writeSitemapXML(w, index)
}

// sitemapFile serves one sitemap of the index: /sitemaps/{name}.xml.
func (s *server) sitemapFile(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutSuffix(r.PathValue("file"), ".xml")
	if !s.indexable() || !ok {
		s.renderError(w, r, http.StatusNotFound, "Page not found.")
		return
	}
	parts, err := s.sitemapParts(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, p := range parts {
		if p.Name == name {
			s.writeSitemapXML(w, struct {
				XMLName xml.Name     `xml:"urlset"`
				NS      string       `xml:"xmlns,attr"`
				URLs    []sitemapURL `xml:"url"`
			}{NS: sitemapNS, URLs: p.URLs})
			return
		}
	}
	s.renderError(w, r, http.StatusNotFound, "Page not found.")
}

// pageTitles are the main pages' titles (<title>: search results, link
// previews and the tab) on a published copy, by path, worded the way
// people search; the page's own heading keeps its short name.
var pageTitles = map[string]string{
	"/":          "HuntDeck: where to hunt next across public bug bounty programs",
	"/programs":  "Public bug bounty programs on HackerOne, Bugcrowd, Intigriti and YesWeHack",
	"/stats":     "Bug bounty stats: payouts, reports and response times by platform",
	"/targets":   "Bug bounty targets: in-scope wildcards, domains and URLs",
	"/changes":   "Bug bounty scope changes, as platforms publish them",
	"/disclosed": "Disclosed bug bounty reports",
	"/api":       "Bug bounty scope API: programs, targets and stats",
}

// programTitle is a program page's title in search results: what people
// search for ("acme bug bounty"), then where it runs.
func programTitle(d programData) string {
	name, lower := d.P.Name, strings.ToLower(d.P.Name)
	title := name
	switch {
	case strings.Contains(lower, "program") || strings.HasSuffix(lower, " vdp") || lower == "vdp":
		// It already says what it is: "Acme Bug Bounty Program".
	case strings.Contains(lower, "bounty") || strings.Contains(lower, "disclosure"):
		title += " program" // "Meta Bug Bounty program"
	case d.P.OffersBounty:
		title += " bug bounty program"
	default:
		title += " vulnerability disclosure program"
	}
	if label := platformLabel(d.P.Platform); !strings.Contains(lower, strings.ToLower(label)) {
		title += " (" + label + ")" // not "YesWeHack … (YesWeHack)"
	}
	switch {
	case d.P.Platform == store.Independent && d.P.OffersBounty:
		return title + ": scope, payouts and rules" // no stats for a program added by hand
	case d.P.Platform == store.Independent:
		return title + ": scope and rules"
	case d.P.OffersBounty:
		return title + ": scope, payouts and stats"
	}
	return title + ": scope and stats"
}

// crumb is one step of a page's trail, for search results.
type crumb struct{ Name, Path string }

// structuredData is a published page's JSON-LD for search engines: the
// site on the home page, the trail back on pages that have one. "" for
// neither.
func (s *server) structuredData(path string, crumbs []crumb) template.JS {
	var items []any
	if path == "/" {
		site := map[string]any{"@type": "WebSite", "name": "HuntDeck", "url": s.PublicURL + "/", "description": siteDescription}
		var same []string
		if s.XAccount != "" {
			same = append(same, "https://x.com/"+s.XAccount)
		}
		if s.RepoURL != "" {
			same = append(same, s.RepoURL)
		}
		if same != nil {
			site["sameAs"] = same
		}
		items = append(items, site)
	}
	if len(crumbs) > 0 {
		list := make([]any, len(crumbs))
		for i, c := range crumbs {
			list[i] = map[string]any{"@type": "ListItem", "position": i + 1, "name": c.Name, "item": s.PublicURL + c.Path}
		}
		items = append(items, map[string]any{"@type": "BreadcrumbList", "itemListElement": list})
	}
	if len(items) == 0 {
		return ""
	}
	// encoding/json escapes <, > and &, so nothing in it ends the script.
	b, err := json.Marshal(map[string]any{"@context": "https://schema.org", "@graph": items})
	if err != nil {
		return ""
	}
	return template.JS(b) //nolint:gosec // JSON with <, > and & escaped, in a data block
}

// siteDescription describes HuntDeck where a page has nothing of its own.
const siteDescription = "Live scope, changes and payouts for every public bug bounty program on HackerOne, Bugcrowd, Intigriti and YesWeHack, ranked by where to hunt next."

// pageDescriptions describe the main pages, by their nav key.
var pageDescriptions = map[string]string{
	"dashboard":   "A hunt board for bug bounty hunters: fresh attack surface, top picks and the latest changes across HackerOne, Bugcrowd, Intigriti and YesWeHack.",
	"programs":    "Every public bug bounty program on HackerOne, Bugcrowd, Intigriti and YesWeHack, ranked by HuntScore, with bounties, payouts, response times and new scope.",
	"stats":       "Compare bug bounty platforms and programs: bounties paid, reports received, valid reports and response times over time.",
	"targets":     "In-scope targets from public bug bounty programs: wildcards, domains, URLs, apps and IP ranges, filterable and ready to copy.",
	"changes":     "Scope, bounty and policy changes across public bug bounty programs, as the platforms publish them.",
	"disclosed":   "Publicly disclosed bug bounty reports, across programs and platforms.",
	"methodology": "How HuntDeck's numbers are worked out: where each comes from on each platform, what is an estimate, and how HuntScore ranks programs.",
	"api":         "HuntDeck's REST and plain-text API: programs, scope, targets and stats from public bug bounty programs.",
}

// programDescription describes a program page.
func programDescription(d programData) string {
	p := d.P
	kind := "vulnerability disclosure program"
	if p.OffersBounty {
		kind = "bug bounty program"
	}
	var b strings.Builder
	b.WriteString(p.Name + ": " + platformLabel(p.Platform) + " " + kind + ". ")
	b.WriteString(strconv.Itoa(len(d.InScope)) + plural(len(d.InScope), " in-scope target", " in-scope targets"))
	if n := len(d.Wildcards); n > 0 {
		b.WriteString(", " + strconv.Itoa(n) + plural(n, " wildcard", " wildcards"))
	}
	if rng := bountyWords(d.Row.BountyMin, d.Row.BountyMax, d.Row.Currency); rng != "" && p.OffersBounty {
		b.WriteString(", bounties " + rng)
	}
	if p.Platform == store.Independent {
		b.WriteString(". Scope and rules, added by hand.")
	} else {
		b.WriteString(". Scope, changes, payouts and stats, kept up to date.")
	}
	return b.String()
}

// bountyWords is a bounty range in words, as the page shows it: "$100 –
// $15k", "up to $5k" with only a top, "from $500" with only a lowest.
func bountyWords(lo, hi *float64, currency string) string {
	rng := amountRange(lo, hi, currency)
	switch {
	case rng == "":
	case lo == nil:
		return "up to " + rng
	case hi == nil:
		return "from " + rng
	}
	return rng
}
