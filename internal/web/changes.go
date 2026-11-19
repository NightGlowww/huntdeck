package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

type changesData struct {
	Since     string
	Group     string
	Scope     string // with group=scope: "added", "removed" or "edited" ("" for all three)
	Change    string // with group=programs: one of programKinds' keys ("" for all)
	Sort      string // "" (by date), "name" or "platform"
	Reverse   bool
	Bounty    string // "1" (paying programs), "vdp" or "" (both), as on Programs
	Platform  string
	Changes   []store.ChangeView
	Pager     pager
	Platforms []string
	Counts    []store.ChangeCount // by platform, kind and period (changePeriods' order)
	query     url.Values
}

// changePeriods are the Changes page's periods, newest first; all time is
// the last.
var changePeriods = []struct {
	Key, Label string
	Within     time.Duration
}{{"24h", "Last 24 hours", 24 * time.Hour}, {"7d", "Last 7 days", 7 * 24 * time.Hour}, {"30d", "Last 30 days", 30 * 24 * time.Hour}, {"all", "All time", 0}}

// changeKinds are the Changes page's kinds of change, as ?group= values.
var changeKinds = []struct{ Key, Label, Title string }{
	{"new", "New programs", "Programs that appeared, or came back"},
	{"programs", "Program changes", "New and removed programs, status, visibility, bounties, renames"},
	{"scope", "Scope changes", "Assets added, removed or edited"},
	{"rules", "Policy and rewards", "Policy text and bounty table edits"},
	{"announcements", "Announcements", "Programs' posts to researchers"},
}

// scopeKinds narrow group=scope to one kind of scope change, as ?scope=
// values.
var scopeKinds = []struct{ Key, Kind, Label, Title string }{
	{"added", store.ChangeScopeAdded, "Assets added", "Assets new to a program's scope"},
	{"removed", store.ChangeScopeRemoved, "Assets removed", "Assets gone from a program's scope"},
	{"edited", store.ChangeScopeUpdated, "Assets edited", "Assets whose scope, type, severity or bounty eligibility changed"},
}

// programKinds narrow group=programs to one kind of program change, as
// ?change= values; a state change is split by where the program went.
var programKinds = []struct {
	Key, Kind string
	To        []string // a state change's new states
	Label     string
	Title     string
}{
	{"added", store.ChangeProgramAdded, nil, "New programs", "Programs that appeared, or came back"},
	{"removed", store.ChangeProgramRemoved, nil, "Removed programs", "Programs the platform stopped listing"},
	{"paused", store.ChangeStateChanged, []string{"paused", "closed"}, "Paused or closed", "Programs that stopped taking reports"},
	{"opened", store.ChangeStateChanged, []string{"open"}, "Opened or resumed", "Programs that went back to taking reports"},
	{"bounty", store.ChangeBountyChanged, nil, "Bounty changes", "Started or stopped paying bounties"},
	{"visibility", store.ChangeVisibilityChanged, nil, "Visibility", "Went public or private"},
	{"renamed", store.ChangeProgramRenamed, nil, "Renamed", "New names"},
}

// changeKindsFor are the change kinds group and scope (see scopeKinds)
// or change (programKinds) list, nil for all, and the new values a state
// change must have, nil for any.
func changeKindsFor(group, scope, change string) ([]string, []string) {
	switch group {
	case "scope":
		for _, k := range scopeKinds {
			if k.Key == scope {
				return []string{k.Kind}, nil
			}
		}
	case "programs":
		for _, k := range programKinds {
			if k.Key == change {
				return []string{k.Kind}, k.To
			}
		}
	}
	return changeGroups[group], nil
}

func (d changesData) list() listQuery { return listQuery{"/changes", d.query} }

// count is how many changes match platform ("" for all), kinds (nil for
// all), bounty ("1" for paying programs, "vdp" for the rest, "" for both)
// and the period at index period.
func (d changesData) count(platform string, kinds []string, bounty string, period int) int {
	return d.countTo(platform, kinds, nil, bounty, period)
}

// countTo is count for state changes to one of to (nil for any).
func (d changesData) countTo(platform string, kinds, to []string, bounty string, period int) int {
	n := 0
	for _, c := range d.Counts {
		if (platform == "" || c.Platform == platform) && (kinds == nil || slices.Contains(kinds, c.Kind)) &&
			(to == nil || c.Kind != store.ChangeStateChanged || slices.Contains(to, c.To)) &&
			(bounty == "" || c.Bounty == (bounty == "1")) && (changePeriods[period].Within == 0 || c.Bucket <= period) {
			n += c.N
		}
	}
	return n
}

// period is the index of the chosen period.
func (d changesData) period() int {
	for i, p := range changePeriods {
		if p.Key == d.Since {
			return i
		}
	}
	return 1
}

// Rail is the filter rail's groups. Choosing a kind drops the scope
// sub-filter, which only group=scope has.
func (d changesData) Rail() []railGroup {
	l, per := d.list(), d.period()
	kinds, to := changeKindsFor(d.Group, d.Scope, d.Change)
	period := railGroup{Title: "Period"}
	for i, p := range changePeriods {
		o := l.choice(p.Label, "", "since", p.Key, d.Since == p.Key, d.countTo(d.Platform, kinds, to, d.Bounty, i), true)
		if p.Key == "7d" {
			o = l.fallback(p.Label, "", "since", d.Since == p.Key, o.Count, true)
		}
		period.Options = append(period.Options, o)
	}
	kind, lk := railGroup{Title: "Kind"}, l.without("scope", "change")
	for _, k := range changeKinds {
		kind.Options = append(kind.Options, lk.option(k.Label, k.Title, "group", k.Key, d.count(d.Platform, changeGroups[k.Key], d.Bounty, per), true))
	}
	groups := []railGroup{watchGroup(l, len(watchParam(d.query.Get("watch"))) > 0, false, false), period, kind}
	if d.Group == "scope" {
		g := railGroup{Title: "Scope changes"}
		for _, k := range scopeKinds {
			g.Options = append(g.Options, l.option(k.Label, k.Title, "scope", k.Key, d.count(d.Platform, []string{k.Kind}, d.Bounty, per), true))
		}
		groups = append(groups, g)
	}
	if d.Group == "programs" {
		g := railGroup{Title: "Program changes"}
		for _, k := range programKinds {
			g.Options = append(g.Options, l.option(k.Label, k.Title, "change", k.Key, d.countTo(d.Platform, []string{k.Kind}, k.To, d.Bounty, per), true))
		}
		groups = append(groups, g)
	}
	groups = append(groups, railGroup{Title: "Rewards", Options: []railOption{
		l.option("Pays bounties", "Changes to programs that pay bounties", "bounty", "1", d.countTo(d.Platform, kinds, to, "1", per), true),
		l.option("VDP only", "Changes to programs that don't pay bounties", "bounty", "vdp", d.countTo(d.Platform, kinds, to, "vdp", per), true),
	}})
	if len(d.Platforms) > 1 {
		g := railGroup{Title: "Platform"}
		for _, p := range d.Platforms {
			o := l.option(platformLabel(p), "", "platform", p, d.countTo(p, kinds, to, d.Bounty, per), true)
			o.Platform = p
			g.Options = append(g.Options, o)
		}
		groups = append(groups, g)
	}
	return groups
}

// RailView is the filter rail (no search: changes are found by program).
func (d changesData) RailView() railView {
	return railView{Path: "/changes", Groups: d.Rail(), Views: d.Views()}
}

// Pills are the active filters.
func (d changesData) Pills() []pill { return d.list().pills(d.Rail()) }

// ClearURL drops every filter, keeping the order.
func (d changesData) ClearURL() string { return d.list().keeping("sort", "rev") }

// changeSorts are the Changes page's orders, as ?sort= values.
var changeSorts = []struct{ Key, Label, Title string }{
	{"", "Date", "Newest first; again for oldest first"},
	{store.ChangesByName, "Program", "By program name, A to Z; again for Z to A"},
	{store.ChangesByPlatform, "Platform", "By platform, then newest first"},
}

// sortLink is one of the list's orders: choosing the current one again
// turns it around.
type sortLink struct {
	Label, Title, URL string
	On                bool
	Dir               string // the current order's direction in words: "newest first", "Z–A"
}

// Sorts are the list's orders.
func (d changesData) Sorts() []sortLink {
	var out []sortLink
	for _, s := range changeSorts {
		q := listQuery{d.list().path, url.Values{}}
		for k, v := range d.query {
			if k != "rev" && k != "sort" {
				q.query[k] = v
			}
		}
		on := d.Sort == s.Key
		if s.Key != "" {
			q.query.Set("sort", s.Key)
		}
		if on && !d.Reverse {
			q.query.Set("rev", "1")
		}
		l := sortLink{Label: s.Label, Title: s.Title, URL: q.with("page", ""), On: on}
		if on {
			l.Dir = map[[2]bool]string{{true, false}: "newest first", {true, true}: "oldest first", {false, false}: "A–Z", {false, true}: "Z–A"}[[2]bool{s.Key == "", d.Reverse}]
		}
		out = append(out, l)
	}
	return out
}

// Period names the chosen period, for the list's heading.
func (d changesData) Period() string { return strings.ToLower(changePeriods[d.period()].Label) }

var changeGroups = map[string][]string{
	"programs": {store.ChangeProgramAdded, store.ChangeProgramRemoved, store.ChangeVisibilityChanged,
		store.ChangeStateChanged, store.ChangeBountyChanged, store.ChangeProgramRenamed},
	"new":           {store.ChangeProgramAdded},
	"scope":         {store.ChangeScopeAdded, store.ChangeScopeRemoved, store.ChangeScopeUpdated},
	"rules":         {store.ChangePolicyChanged, store.ChangeRewardsChanged},
	"announcements": {store.ChangeAnnouncement},
}

type changeData struct {
	C       store.ChangeView
	Label   string // what changed: "Policy", "Bounty table"…
	Lines   []diffLine
	Added   int
	Removed int
	Rewards []rewardChange
	Body    string // an announcement's text (Markdown)
}

// change shows one policy or bounty table change as a diff.
func (s *server) change(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.renderError(w, r, http.StatusNotFound, "No such change.")
		return
	}
	list, _, err := s.Store.Changes(r.Context(), store.ChangeFilter{ID: id, Limit: 1})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if len(list) == 0 || !hasDetail(list[0].Kind) {
		s.renderError(w, r, http.StatusNotFound, "No such change.")
		return
	}
	d := changeData{C: list[0], Label: ruleLabels[list[0].Field]}
	switch d.C.Kind {
	case store.ChangeAnnouncement:
		title, body, _ := strings.Cut(d.C.NewValue, "\n")
		d.Label, d.Body = strings.TrimSpace(title), strings.TrimSpace(body)
		s.render(w, r, http.StatusOK, "change", pageData{Title: "Announcement · " + d.C.Program.Name, Nav: "changes", Data: d})
		return
	case store.ChangeRewardsChanged:
		d.Label, d.Rewards = "Bounty table", rewardsDiff(d.C.OldValue, d.C.NewValue, d.C.Currency)
	default:
		d.Lines, d.Added, d.Removed = textDiff(d.C.OldValue, d.C.NewValue)
	}
	s.render(w, r, http.StatusOK, "change", pageData{Title: d.Label + " · " + d.C.Program.Name, Nav: "changes", Data: d})
}

// changeCounts tallies changes by platform, kind and period
// (changePeriods), memoised: it reads every change there has been.
func (s *server) changeCounts(ctx context.Context, watch []store.ProgramKey) ([]store.ChangeCount, error) {
	return store.Memo(ctx, s.Store, "change-counts?"+watchKey(watch), func(ctx context.Context) ([]store.ChangeCount, error) {
		now := time.Now()
		var cutoffs []time.Time
		for _, p := range changePeriods {
			if p.Within > 0 {
				cutoffs = append(cutoffs, now.Add(-p.Within))
			}
		}
		return s.Store.ChangeCounts(ctx, cutoffs, watch)
	})
}

// changeParams are the Changes page's filters as given.
type changeParams struct{ since, group, scope, change, bounty string }

// changeFilterFrom reads the Changes page's filters, which its feed shares.
func changeFilterFrom(q url.Values) (store.ChangeFilter, changeParams) {
	since := oneOf(q.Get("since"), "7d", "24h", "30d", "all")
	group := oneOf(q.Get("group"), "", "new", "programs", "scope", "rules", "announcements")
	scope, change := "", ""
	if group == "scope" {
		scope = oneOf(q.Get("scope"), "", "added", "removed", "edited")
	}
	if group == "programs" {
		for _, k := range programKinds {
			if q.Get("change") == k.Key {
				change = k.Key
			}
		}
	}
	bounty := ""
	if isTrue(q.Get("bounty")) {
		bounty = "1"
	} else if q.Get("bounty") == "vdp" {
		bounty = "vdp"
	}
	kinds, to := changeKindsFor(group, scope, change)
	f := store.ChangeFilter{Kinds: kinds, NewValues: to, Platform: canonicalPlatform(q.Get("platform")),
		BountyOnly: bounty == "1", NoBounty: bounty == "vdp", Newest: true, CollapseFlips: true, Watch: watchParam(q.Get("watch"))}
	for _, p := range changePeriods {
		if p.Key == since && p.Within > 0 {
			f.Since = time.Now().Add(-p.Within)
		}
	}
	return f, changeParams{since, group, scope, change, bounty}
}

func (s *server) changes(w http.ResponseWriter, r *http.Request) {
	const perPage = 100
	q := r.URL.Query()
	// format=txt is the header's copy and download link: every change these
	// filters match, one a line.
	if q.Get("format") == "txt" {
		s.changesText(w, r)
		return
	}
	f, cp := changeFilterFrom(q)
	f.Order, f.Reverse = oneOf(q.Get("sort"), "", store.ChangesByName, store.ChangesByPlatform), q.Get("rev") == "1"
	if f.Order == "" && f.Reverse {
		f.Newest = false // by date, oldest first
	}
	f.Limit, f.Offset = perPage, (pageFrom(r)-1)*perPage
	list, total, err := s.Store.Changes(r.Context(), f)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	counts, err := s.changeCounts(r.Context(), f.Watch)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	d := changesData{
		Since: cp.since, Group: cp.group, Scope: cp.scope, Change: cp.change, Sort: f.Order, Reverse: f.Reverse, Bounty: cp.bounty, Platform: f.Platform, Changes: list, Pager: newPager(r, perPage, total),
		Platforms: s.listPlatforms(r.Context()), Counts: counts, query: q,
	}
	s.render(w, r, http.StatusOK, "changes", pageData{Title: "Changes", Nav: "changes", Feed: d.FeedURL(), FeedTitle: "HuntDeck: changes", Data: d})
}

// ShowingScope reports whether the page keeps only scope changes.
func (d changesData) ShowingScope() bool { return d.Group == "scope" }

// ScopeURL switches to scope changes only, or back to every kind.
func (d changesData) ScopeURL() string {
	if d.ShowingScope() {
		return d.list().with("group", "")
	}
	return d.list().with("group", "scope")
}

// changeViewKeys are what a saved view of the Changes page keeps.
var changeViewKeys = []string{"platform", "group", "scope", "change", "since", "bounty", "sort", "rev", "watch"}

// Views is the Changes page's saved views.
func (d changesData) Views() *myViews { return newMyViews("/changes", changeViewKeys, "") }

// ScopeCount is how many scope changes the current platform, period and
// bounty filters hold, for the header chip.
func (d changesData) ScopeCount() int {
	return d.count(d.Platform, changeGroups["scope"], d.Bounty, d.period())
}

// changesText answers the page's copy and download links: one change a line,
// as "date<TAB>program<TAB>what happened". Every matching change, not the page
// of them.
func (s *server) changesText(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f, _ := changeFilterFrom(q)
	f.Order, f.Reverse = oneOf(q.Get("sort"), "", store.ChangesByName, store.ChangesByPlatform), q.Get("rev") == "1"
	if f.Order == "" && f.Reverse {
		f.Newest = false
	}
	f.Limit, f.Offset = 5000, 0
	rows, _, err := s.Store.Changes(r.Context(), f)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	for _, c := range rows {
		// Four columns: when, which program, what happened, and the asset it
		// is about (empty for a change to the program itself).
		//nolint:gosec // the response is text/plain and the values are tab-joined, not markup
		_, _ = fmt.Fprintf(w, "%s\t%s/%s\t%s\t%s\n", c.OccurredAt.Format("2006-01-02"), c.Program.Platform, c.Program.Handle,
			strings.Join(strings.Fields(describeChangeLang(s.langOf(r), s.messages, c)), " "), oneLine(c.Asset))
	}
}

// oneLine keeps a value on its line in a tab-separated export.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// ExportURL is this view as plain text: one change a line, every match.
func (d changesData) ExportURL() string {
	v := url.Values{}
	for k, vs := range d.query {
		if k != "page" && k != "format" {
			v[k] = vs
		}
	}
	v.Set("format", "txt")
	return "/changes?" + v.Encode()
}
