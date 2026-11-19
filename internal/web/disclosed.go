package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// severityChoices are the Disclosed page's severity filter values.
var severityChoices = map[string][]string{
	"critical": {"critical"}, "high": {"high"}, "high+": {"critical", "high"},
	"medium": {"medium"}, "low": {"low"}, "none": {"none", ""},
}

// disclosureFilterFrom reads the Disclosed page and API filters.
func disclosureFilterFrom(q url.Values, now time.Time) store.DisclosureFilter {
	return store.DisclosureFilter{
		Platform: canonicalPlatform(q.Get("platform")),
		Query:    strings.TrimSpace(q.Get("q")),
		Severity: severityChoices[q.Get("severity")],
		Weakness: q.Get("weakness"),
		PaidOnly: isTrue(q.Get("paid")),
		WithCVE:  isTrue(q.Get("cve")),
		Since:    addedSince(q.Get("since"), now),
		Sort:     oneOf(q.Get("sort"), "new", "bounty", "votes"),
	}
}

type disclosedData struct {
	Filter    store.DisclosureFilter
	Severity  string // as given
	Since     string
	Rows      []store.DisclosureRow
	Pager     pager
	Facets    store.DisclosureFacets
	Platforms []string
	query     url.Values
}

// disclosedSince are the Disclosed page's "disclosed in the last" choices.
var disclosedSince = []string{"7d", "30d", "90d", "365d"}

// severityOptions are the Disclosed page's severities, as ?severity= values.
var severityOptions = []struct{ Key, Label string }{
	{"critical", "Critical"}, {"high+", "Critical and high"}, {"high", "High"}, {"medium", "Medium"}, {"low", "Low"}, {"none", "None or unrated"},
}

// railWeaknesses is how many weaknesses the rail offers.
const railWeaknesses = 10

func (d disclosedData) list() listQuery { return listQuery{"/disclosed", d.query} }

// Rail is the filter rail's groups.
func (d disclosedData) Rail() []railGroup {
	l, fc := d.list(), d.Facets
	since := railGroup{Title: "Disclosed"}
	for i, v := range disclosedSince {
		o := l.option("Last "+addedLabels[v], "", "since", v, 0, false)
		if i < len(fc.Since) {
			o.Count, o.Counted = fc.Since[i], true
		}
		since.Options = append(since.Options, o)
	}
	sev := railGroup{Title: "Severity"}
	for _, o := range severityOptions {
		n := 0
		for _, v := range severityChoices[o.Key] {
			n += fc.Severities[v]
		}
		sev.Options = append(sev.Options, l.option(o.Label, "", "severity", o.Key, n, true))
	}
	groups := []railGroup{since, sev}
	// Only platforms with disclosed reports (HackerOne publishes them; the
	// others hardly do), and none if that's just one.
	g := railGroup{Title: "Platform"}
	for _, p := range d.Platforms {
		if fc.Platforms[p] > 0 || p == d.Filter.Platform {
			o := l.option(platformLabel(p), "", "platform", p, fc.Platforms[p], true)
			o.Platform = p
			g.Options = append(g.Options, o)
		}
	}
	if len(g.Options) > 1 || d.Filter.Platform != "" {
		groups = append(groups, g)
	}
	weak := railGroup{Title: "Weakness", Long: true}
	shown := false
	for _, w := range fc.Weaknesses {
		o := l.option(w.Name, w.Name, "weakness", w.Name, w.Count, true)
		shown = shown || o.On
		weak.Options = append(weak.Options, o)
	}
	if w := d.Filter.Weakness; w != "" && !shown { // chosen from a report, beyond the top ones
		weak.Options = append(weak.Options, l.option(w, w, "weakness", w, 0, false))
	}
	return append(groups, weak, railGroup{Title: "Bounty", Options: []railOption{
		l.option("With a bounty", "Reports whose bounty is public", "paid", "1", fc.Paid, true),
		l.option("With a CVE", "Reports that name a CVE", "cve", "1", fc.WithCVE, true),
	}})
}

// RailView is the filter rail with its search box.
func (d disclosedData) RailView() railView {
	return railView{Path: "/disclosed", Search: d.Filter.Query, Placeholder: "Title or program", Label: "Search reports",
		Hidden: d.list().hidden("q"), Groups: d.Rail()}
}

// Pills are the active filters.
func (d disclosedData) Pills() []pill { return d.list().pills(d.Rail()) }

// ClearURL drops every filter, keeping the sort.
func (d disclosedData) ClearURL() string { return d.list().keeping("sort") }

// SortURL links to the same filters sorted by key, from page 1.
func (d disclosedData) SortURL(key string) string {
	if key == "new" {
		key = ""
	}
	return d.list().with("sort", key)
}

func (s *server) disclosed(w http.ResponseWriter, r *http.Request) {
	const perPage = 100
	q := r.URL.Query()
	now := s.now(r.Context())
	f := disclosureFilterFrom(q, now)
	f.Limit, f.Offset = perPage, (pageFrom(r)-1)*perPage
	// ?format=txt: the reports this filter shows, one URL per line, for the
	// page's copy and download links.
	if q.Get("format") == "txt" {
		s.writeDisclosedText(w, r, f)
		return
	}
	t0 := time.Now()
	rows, total, err := s.Store.Disclosures(r.Context(), f)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	tRows := time.Since(t0)
	cutoffs := make([]time.Time, len(disclosedSince))
	for i, v := range disclosedSince {
		cutoffs[i] = addedSince(v, now)
	}
	facets, err := store.Memo(r.Context(), s.Store, "disclosure-facets:"+facetKey(q, disclosureFacetParams...), func(ctx context.Context) (store.DisclosureFacets, error) {
		return s.Store.DisclosureFacetsFor(ctx, f, cutoffs, railWeaknesses)
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	tData := time.Since(t0)
	s.render(w, r, http.StatusOK, "disclosed", pageData{Title: "Disclosed reports", Nav: "disclosed", Data: disclosedData{
		Filter: f, Severity: q.Get("severity"), Since: q.Get("since"), Rows: rows, Pager: newPager(r, perPage, total),
		Facets: facets, Platforms: s.platformNames(), query: q,
	}})
	s.Log.Debug("disclosed served", "rows", tRows.Round(time.Millisecond), "counts", (tData - tRows).Round(time.Millisecond),
		"render", (time.Since(t0) - tData).Round(time.Millisecond))
}

// apiDisclosed lists disclosed reports across programs, newest first by
// default, with the Disclosed page's filters (limit, default 100, max 1000;
// offset).
func (s *server) apiDisclosed(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := disclosureFilterFrom(q, s.now(r.Context()))
	f.Limit = clampInt(q.Get("limit"), 100, 1, 1000)
	f.Offset = clampInt(q.Get("offset"), 0, 0, 1<<30)
	rows, total, err := s.Store.Disclosures(r.Context(), f)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	out := apiDisclosedListJSON{Total: total, Reports: []apiDisclosedRow{}}
	for _, d := range rows {
		out.Reports = append(out.Reports, apiDisclosedRow{disclosureJSON{d.ReportID, d.Title, d.URL, d.Severity, d.Weakness, d.CVEIDs,
			d.Substate, d.Bounty, d.SubmittedAt, d.DisclosedAt, d.Votes, d.Reporter, d.Summary}, d.Currency, refJSON(d.Program)})
	}
	writeJSON(w, http.StatusOK, out)
}

// WeaknessURL is the list filtered to weakness w, keeping the other filters.
func (d disclosedData) WeaknessURL(w string) string { return d.list().with("weakness", w) }

// SortAria is a disclosed report heading's aria-sort: newest, highest
// bounty or most votes first, all descending.
func (d disclosedData) SortAria(key string) string {
	sort := d.Filter.Sort
	if sort == "" {
		sort = "new"
	}
	if sort != key {
		return ""
	}
	return "descending"
}

// writeDisclosedText answers the page's copy and download links: the
// reports the filter shows, one URL per line (the whole filter, not the
// page of it).
func (s *server) writeDisclosedText(w http.ResponseWriter, r *http.Request, f store.DisclosureFilter) {
	f.Limit, f.Offset = 5000, 0
	rows, _, err := s.Store.Disclosures(r.Context(), f)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	for _, d := range rows {
		_, _ = fmt.Fprintln(w, d.URL)
	}
}

// ExportURL is this list as plain text, one report URL per line, with the
// filters the page has.
func (d disclosedData) ExportURL() string { return d.list().with("format", "txt") }
