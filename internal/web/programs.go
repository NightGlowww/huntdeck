package web

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/store"
)

// programView is a built-in view of the programs page: a filter and sort
// with a name, at the top of the filter rail. Query is url.Values-encoded (keys sorted) so the current
// page's query can be compared with it.
type programView struct{ Label, Title, Query string }

var programViews = []programView{
	{"Best to hunt", "Highest HuntScore among paying programs", "bounty=1&sort=huntscore"},
	{"Fresh scope", "Paying programs that added scope in the last 30 days, most added first", "bounty=1&new=30d&sort=fresh"},
	{"Big bounties", "Highest top bounty first", "bounty=1&sort=bounty"},
	{"Quiet programs", "Least competition first, by HuntScore: few reports for the scope's size, or invite-only", "bounty=1&sort=quiet"},
	{"Fast response", "Quickest first response (HackerOne, YesWeHack) or validation (Bugcrowd) first", "bounty=1&sort=response"},
	{"New launches", "Most recently launched first", "sort=launched"},
	// Back open and private programs are the rail's own choices.
}

// viewKeeps are the parameters views keep, besides the platforms, so a
// view narrows what they chose: the search first (as typed), excluded
// words, the watchlist and a minimum top bounty (Quiet programs paying
// 5k or more).
var viewKeeps = []string{"q", "not", "watch", "noted", "top"}

// programsDefaultSort is the Programs page's order without ?sort=: the
// best to hunt first.
const programsDefaultSort = "huntscore"

// programSortKeys are the sort keys programFilterFrom accepts, the API's
// default first. Column headings and views link to them; wildcards, domains,
// resumed and new have no heading of their own.
func programSortKeys() []string {
	return []string{"name", "huntscore", "invited", "bounty", "paid90", "paidall", "avgbounty", "quiet", "response", "fresh", "resolved", "received", "wildcards", "domains", "scope", "launched", "updated", "resumed", "new", "disclosed"}
}

// Day windows the programs page's filters take, as ?new=, ?launched= and
// ?resumed= values.
var (
	newScopeOptions = map[string]time.Duration{"7d": store.FacetNewScope[0], "30d": store.FacetNewScope[1]}
	launchedOptions = map[string]time.Duration{"90d": store.FacetLaunched}
)

// since reads a day window from options; anything else means no filter.
func since(options map[string]time.Duration, v string, now time.Time) time.Time {
	if d, ok := options[v]; ok {
		return now.Add(-d)
	}
	return time.Time{}
}

// minBounty reads ?top=: one of store.FacetTopBounties (the rail's chips)
// or any amount typed in beside them, up to maxMinBounty; "5k" reads as
// 5000.
func minBounty(v string) float64 {
	v = strings.ToLower(strings.TrimSpace(strings.NewReplacer(",", "", "$", "", "_", "").Replace(v)))
	mult := 1.0
	if s, ok := strings.CutSuffix(v, "k"); ok {
		v, mult = s, 1000
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil || n <= 0 || math.IsInf(n, 0) || math.IsNaN(n) {
		return 0
	}
	return min(math.Round(n*mult), maxMinBounty)
}

// maxMinBounty is the largest minimum bounty ?top= takes.
const maxMinBounty = 10_000_000

// list is the page's query, for links that change a filter.
func (d programsData) list() listQuery { return listQuery{d.Path, d.query} }

// queryWith is the programs page's current query with key set to value
// (dropped if value is ""), back on its first page.
func (d programsData) queryWith(key, value string) string { return d.list().with(key, value) }

// option is a rail choice setting key to value.
func (d programsData) option(label, title, key, value string, count int, counted bool) railOption {
	return d.list().option(label, title, key, value, count, counted)
}

// openOption is the Status group's "Open", the list's default: on unless
// another status is chosen, and choosing it goes back to the default.
func (d programsData) openOption() railOption {
	on := d.query.Get("state") == "" || d.query.Get("state") == "open"
	return d.list().fallback("Open", "Taking reports now (the default)", "state", on, 0, false)
}

// Rail is the filter rail's groups, with counts from d.Facets.
func (d programsData) Rail() []railGroup {
	fc := d.Facets
	groups := []railGroup{watchGroup(d.list(), len(d.Filter.Watch) > 0, len(d.Filter.Noted) > 0, true)}
	if len(d.Platforms) > 1 {
		groups = append(groups, railGroup{Title: "Platform", Options: d.platformOptions()})
	}
	if d.Invites {
		groups = append(groups, railGroup{Title: "Invitation", Options: []railOption{
			d.option("Not accepted yet", "Invitations waiting for you to accept them on the platform", "invite", "pending", fc.InvitesPending, true),
			d.option("Accepted", "Private programs you've joined", "invite", "joined", fc.InvitesJoined, true),
		}})
	}
	groups = append(groups,
		railGroup{Title: "Rewards", Options: []railOption{
			d.option("Pays bounties", "", "bounty", "1", fc.Bounty, true),
			d.option("VDP only", "Programs that don't pay bounties", "bounty", "vdp", fc.VDP, true),
		}},
		railGroup{Title: "Top bounty", Options: d.topOptions(), Form: d.topForm()},
		railGroup{Title: "Attack surface", Options: []railOption{
			d.option("Has wildcards", "", "wild", "1", fc.Wildcards, true),
			d.option("Android or iOS apps", "", "apps", "1", fc.Apps, true),
			d.option("APIs", "", "api", "1", fc.APIs, true),
			d.option("Test credentials", "Hands out test credentials (known once its stats are fetched)", "creds", "1", fc.Credentials, true),
		}},
		railGroup{Title: "Activity", Options: []railOption{
			d.option("New scope · 7 days", "In-scope assets added in the last 7 days", "new", "7d", fc.NewScope[store.FacetNewScope[0]], true),
			d.option("New scope · 30 days", "In-scope assets added in the last 30 days", "new", "30d", fc.NewScope[store.FacetNewScope[1]], true),
			d.option("Launched · 90 days", "Launched in the last 90 days, where the platform says when", "launched", "90d", fc.Launched, true),
			d.option("Back open · 30 days", "Went from paused or closed back to open in the last 30 days", "resumed", "30d", fc.Resumed, true),
		}},
		railGroup{Title: "Requirements", Options: []railOption{
			d.option("No requirements", "Known to need nothing beyond an account: no Signal minimum, ID check, clearance or application", "noreq", "1", fc.NoRequirements, true),
		}},
		railGroup{Title: "Status", Options: []railOption{
			d.openOption(),
			d.option("Paused or closed", "", "state", "paused", 0, false),
			d.option("Open, paused and closed", "", "state", "all", 0, false),
			d.option("No longer listed", "", "state", "removed", 0, false),
		}},
	)
	if !d.Invites { // every invite is private
		groups = append(groups, railGroup{Title: "Visibility", Options: []railOption{
			d.option("Private", "", "visibility", "private", 0, false),
			d.option("Public", "", "visibility", "public", 0, false),
		}})
	}
	groups = append(groups,
		railGroup{Title: "Ignored", Options: []railOption{
			d.option("Include ignored", "Programs you ignore are left out of lists, exports and picks", "ignored", "include", fc.WithIgnored, true),
			d.option("Only ignored", "", "ignored", "only", fc.IgnoredOnly, true),
		}},
	)
	if len(d.Tags) > 0 {
		g := railGroup{Title: "Tags"}
		for _, t := range d.Tags {
			g.Options = append(g.Options, d.option(t.Tag, "", "tag", t.Tag, t.Programs, true))
		}
		groups = append(groups, g)
	}
	return groups
}

// topForm is the Top bounty group's own amount, beside its chips.
func (d programsData) topForm() *railForm {
	f := &railForm{Path: d.Path, Name: "top", Placeholder: "Custom minimum", Label: "Top bounty at least", Hidden: d.list().hidden("top"),
		Title: "A top bounty of at least this much, in the program's own currency: 2500 or 2.5k"}
	if v := d.Filter.MinBounty; v > 0 && !slices.Contains(store.FacetTopBounties, v) {
		f.Value = strconv.FormatFloat(v, 'f', -1, 64)
	}
	return f
}

func (d programsData) topOptions() []railOption {
	var out []railOption
	for _, v := range store.FacetTopBounties {
		label := fmt.Sprintf("%gk or more", v/1000)
		// On for the amount however it was typed ("5k", "5000").
		out = append(out, d.list().choice(label, "Top bounty, in the program's own currency", "top", strconv.FormatFloat(v, 'f', -1, 64),
			d.Filter.MinBounty == v, d.Facets.TopBounty[v], true))
	}
	return out
}

// Pills are the active filters, each with a link that drops it.
func (d programsData) Pills() []pill {
	out := d.list().pills(d.Rail())
	if v := d.Filter.MinBounty; v > 0 && !slices.Contains(store.FacetTopBounties, v) {
		out = append(out, pill{Label: "Top bounty " + strings.TrimPrefix(humanize.Money(v, ""), "$") + " or more", URL: d.queryWith("top", "")})
	}
	if not := strings.TrimSpace(d.query.Get("not")); not != "" {
		out = append(out, pill{Label: "Without “" + not + "”", URL: d.queryWith("not", "")})
	}
	// The rail offers 30 days; older links can ask for less.
	if l, ok := map[string]string{"24h": "Back open · 24 hours", "7d": "Back open · 7 days"}[d.query.Get("resumed")]; ok {
		out = append(out, pill{Label: l, URL: d.queryWith("resumed", "")})
	}
	return out
}

// ClearURL drops every filter, keeping the search and sort.
func (d programsData) ClearURL() string { return d.list().keeping("sort", "rev") }

// views are the built-in views, as the rail's first group; the one
// matching the current query (the platforms and viewKeeps aside) is on,
// and turning it off clears it. Both keep those.
func (d programsData) views() railGroup {
	kept := url.Values{}
	if s := strings.TrimSpace(d.query.Get("q")); s != "" {
		kept.Set("q", s)
	}
	if p := d.platformParam(); p != "" {
		kept.Set("platform", p)
	}
	for _, k := range viewKeeps[1:] {
		if v := d.query.Get(k); v != "" {
			kept.Set(k, v)
		}
	}
	q := url.Values{}
	for k, v := range d.query {
		if k != "page" && k != "platform" && !slices.Contains(viewKeeps, k) && len(v) > 0 && v[0] != "" {
			q[k] = v
		}
	}
	cur := q.Encode()
	link := func(view string) string {
		u, _ := url.ParseQuery(view) // the views' own, well formed
		for k := range kept {
			u.Set(k, kept.Get(k))
		}
		if len(u) == 0 {
			return d.Path
		}
		return d.Path + "?" + u.Encode()
	}
	g := railGroup{Title: "Views"}
	for _, v := range programViews {
		o := railOption{Label: v.Label, Title: v.Title, URL: link(v.Query), On: v.Query == cur, View: true}
		if o.On {
			o.URL = link("")
		}
		g.Options = append(g.Options, o)
	}
	return g
}

// RailView is the filter rail: the views, then the filters.
func (d programsData) RailView() railView {
	label := "Search programs"
	if d.Invites {
		label = "Search invites"
	}
	return railView{Path: d.Path, Search: d.query.Get("q"), Placeholder: "Name or handle", Label: label,
		Exclude: true, Not: d.query.Get("not"), Views: d.Views(),
		Hidden: d.list().hidden("q", "not"), Groups: append([]railGroup{d.views()}, d.Rail()...)}
}

// programColumns are the Programs list's columns Customize shows, hides
// and reorders, in the table's default order (the program's own column
// always shows, first). Paid, reports received and valid reports sit
// under one "Last 90 days" heading, so they move together, as a group, and
// reorder among themselves. Which show by default follows the table's
// width (15-programs.css): narrower tables leave some out so the rest fit,
// wider ones add Average bounty as a column (the Bounty column's second
// line below that), Hunters, then Launched, Scope changed and Disclosed;
// Customize can show any of them at any width, or hide any column.
var programColumns = columnPicker{Name: "program", Label: "Customize", Icon: "grid", Fixed: "Program", Columns: []tableColumn{
	{Key: "hunt", Label: "HuntScore"}, {Key: "bounty", Label: "Bounty"}, {Key: "ptotal", Label: "Total paid"},
	{Key: "avg", Label: "Average bounty"},
	{Key: "grp", Label: "Last 90 days", Group: []tableColumn{
		{Key: "paid", Label: "Paid"}, {Key: "comp", Label: "Reports received"}, {Key: "resolved", Label: "Valid reports"},
	}},
	{Key: "resp", Label: "Response"}, {Key: "scope", Label: "Assets"}, {Key: "hunters", Label: "Hunters"},
	{Key: "new", Label: "New · 30 days"}, {Key: "launched", Label: "Launched"}, {Key: "changed", Label: "Scope changed"},
	{Key: "discl", Label: "Disclosed"}, {Key: "req", Label: "Requirements"}, {Key: "open", Label: "Open on the platform"},
}}

// ninetyDaySorts are the Last 90 days columns' sorts: each by its
// number, today's 90-day total, which the chart's last point is.
var ninetyDaySorts = []string{"paid90", "received", "resolved"}

// SortHint says, under the column the list is sorted by, what the sort
// uses: for a 90-day column, its 90-day total rather than the chart.
func (d programsData) SortHint(key string) string {
	if d.Filter.Sort != key || !slices.Contains(ninetyDaySorts, key) {
		return ""
	}
	return "by 90-day total"
}

// Columns is the list's column picker.
func (d programsData) Columns() columnPicker { return programColumns }

type programsData struct {
	// Path is the list's page: /programs, or /invites for the private
	// programs the user is invited to (Invites).
	Path     string
	Invites  bool
	Filter   store.ProgramFilter
	Resumed  string // the "resumed in the last" filter as given
	Programs []programRow
	Facets   store.ProgramFacets
	// IgnoredTotal is how many programs the user ignores, whatever the
	// filters: an empty list says so.
	IgnoredTotal int
	Pager        pager
	Platforms    []string
	Tags         []store.TagCount
	// ScopeMentions is how many scope rules contain the name searched for
	// (store.ScopeMentioning), for a link to them on the scope check.
	ScopeMentions int
	// Accepts are the Accept buttons of the rows with a pending
	// invitation HuntDeck can accept, by row.
	Accepts map[int]*acceptForm
	query   url.Values
}

// Accept is row i's Accept button, or nil.
func (d programsData) Accept(i int) *acceptForm { return d.Accepts[i] }

// SortURL is the current list sorted by key, back on the first page; the
// current sort's turns it around. SortClass is its header's class.
func (d programsData) SortURL(key string) string { return sortURL(d.Path, d.query, d.Filter, key) }

func (d programsData) SortClass(key string) string {
	return sortClass(d.Filter.Sort == key, d.Filter.Reverse)
}

// programFilterFrom reads the programs filter shared by the UI and API.
func programFilterFrom(q url.Values) store.ProgramFilter {
	now := time.Now()
	find, exclude := searchTerms(q.Get("q"), q.Get("not"))
	return store.ProgramFilter{
		Exclude:         exclude,
		Platforms:       platformsParam(q.Get("platform")),
		Watch:           watchParam(q.Get("watch")),
		Noted:           watchParam(q.Get("noted")),
		Visibility:      oneOf(q.Get("visibility"), "", "private", "public"),
		Invite:          oneOf(q.Get("invite"), "", "pending", "joined"),
		State:           oneOf(q.Get("state"), "open", "all", "paused", "removed"),
		BountyOnly:      isTrue(q.Get("bounty")),
		NoBounty:        q.Get("bounty") == "vdp",
		CredentialsOnly: isTrue(q.Get("creds")),
		Query:           find,
		ResumedSince:    since(resumedOptions, q.Get("resumed"), now),
		Ignored:         oneOf(q.Get("ignored"), "", "include", "only"),
		Tag:             tagParam(q.Get("tag")),
		MinBounty:       minBounty(q.Get("top")),
		NewScopeSince:   since(newScopeOptions, q.Get("new"), now),
		LaunchedSince:   since(launchedOptions, q.Get("launched"), now),
		Wildcards:       isTrue(q.Get("wild")),
		Apps:            isTrue(q.Get("apps")),
		APIs:            isTrue(q.Get("api")),
		NoRequirements:  isTrue(q.Get("noreq")),
		Sort:            oneOf(q.Get("sort"), programSortKeys()...),
		Reverse:         q.Get("rev") == "1",
	}
}

// resumedOptions are the "resumed in the last" choices on the programs page.
var resumedOptions = map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}

// programList is a page listing programs: Programs, or Invites.
type programList struct {
	Path, Nav, Title string
	Invites          bool   // only private programs: the user's invitations
	DefaultSort      string // without ?sort=
}

var (
	programsList = programList{Path: "/programs", Nav: "programs", Title: "Programs", DefaultSort: programsDefaultSort}
	// Invites lists the private programs, invitations waiting to be
	// accepted first.
	invitesList = programList{Path: "/invites", Nav: "invites", Title: "Invites", Invites: true, DefaultSort: "invited"}
)

func (s *server) programs(w http.ResponseWriter, r *http.Request) { s.programList(w, r, programsList) }

func (s *server) invites(w http.ResponseWriter, r *http.Request) { s.programList(w, r, invitesList) }

func (s *server) programList(w http.ResponseWriter, r *http.Request, l programList) {
	const perPage = 30 // as many as the hunt board shows fresh targets
	t0 := time.Now()
	f := programFilterFrom(r.URL.Query())
	if r.URL.Query().Get("sort") == "" {
		f.Sort = l.DefaultSort // the API keeps its own default
	}
	if l.Invites {
		f.Visibility = "private"
	}
	f.Limit, f.Offset = perPage, (pageFrom(r)-1)*perPage
	progs, total, err := s.Store.Programs(r.Context(), f)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	tList := time.Since(t0)
	tags, err := s.Store.Tags(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	platforms := s.listPlatforms(r.Context())
	facets, err := store.Memo(r.Context(), s.Store, "program-facets:"+l.Path+"?"+facetKey(r.URL.Query(), programFacetParams...), func(ctx context.Context) (store.ProgramFacets, error) {
		return s.Store.ProgramFacets(ctx, f, platforms)
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	tFacets := time.Since(t0)
	rows, err := s.programRows(r.Context(), progs, time.Now())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	tRows := time.Since(t0)
	mentions := 0
	if f.Query != "" && total > 0 { // with none, the empty list links to the scope check
		if _, _, mentions, err = s.Store.ScopeMentioning(r.Context(), f.Query, 0); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	ignoredTotal := 0
	if n, err := s.Store.IgnoredCount(r.Context()); err == nil {
		ignoredTotal = n
	}
	accepts := map[int]*acceptForm{}
	for i, p := range rows {
		if a := s.acceptFor(p.ProgramListItem, backTo(r)); a != nil {
			accepts[i] = a
		}
	}
	tData := time.Since(t0)
	s.render(w, r, http.StatusOK, "programs", pageData{Title: l.Title, Nav: l.Nav, Data: programsData{
		Path: l.Path, Invites: l.Invites, Filter: f, Resumed: r.URL.Query().Get("resumed"), Programs: rows, Facets: facets, IgnoredTotal: ignoredTotal,
		Pager: newPager(r, perPage, total), Platforms: platforms, Tags: tags, ScopeMentions: mentions, Accepts: accepts, query: r.URL.Query(),
	}})
	s.Log.Debug("programs served", "list", tList.Round(time.Millisecond), "facets", (tFacets - tList).Round(time.Millisecond),
		"rows", (tRows - tFacets).Round(time.Millisecond), "rest", (tData - tRows).Round(time.Millisecond),
		"render", (time.Since(t0) - tData).Round(time.Millisecond))
}

// ShowingIgnored reports whether the list shows only ignored programs.
func (d programsData) ShowingIgnored() bool { return d.Filter.Ignored == "only" }

// SortAria is a header's aria-sort value: the direction it sorts in when
// the list is sorted by it, and nothing otherwise. Every column but the
// name sorts biggest or newest first, so that is its ascending false
// case; "rev" turns it around.
func (d programsData) SortAria(key string) string {
	if d.Filter.Sort != key {
		return ""
	}
	descending := key != "name"
	if d.Filter.Reverse {
		descending = !descending
	}
	if descending {
		return "descending"
	}
	return "ascending"
}

// IgnoredOnlyURL shows only the programs the user ignored, keeping the
// filters on the page: the way back to them from an empty list.
func (d programsData) IgnoredOnlyURL() string { return d.list().with("ignored", "only") }
