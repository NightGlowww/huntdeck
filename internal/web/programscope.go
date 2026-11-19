package web

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// scopeView is a program page's Scope tab: one of in scope, out of scope
// or removed, narrowed to an asset type and a search.
type scopeView struct {
	Show  string // "in", "out" or "removed"
	Type  string // an asset type, or "" for all
	Query string
	Order string // "added" lists newest first; removed entries always are
	// Since keeps entries added in the last 7, 30 or 90 days ("" for any
	// age); it doesn't apply to removed entries, which are dated by their
	// removal.
	Since string
	// Segments switch between in, out and removed; Types narrow to an
	// asset type, "All" first.
	Segments   []scopeLink
	Types      []scopeLink
	SinceLinks []scopeLink
	Rows       []store.EntryView
	Total      int  // rows matching, before the limit
	Limited    bool // Rows stops at scopeRowsShown
	// Truncated means even "show all" was capped: the page says so and points at
	// the export instead of offering a link that would not change anything.
	Truncated bool
	AllURL    string
	OrderURL  string
	// CopyURL gives these rows as plain text, one identifier per line.
	CopyURL  string
	ClearURL string // without the type and search
	// Bounty and Severity show those columns; a column whose rows all say
	// the same is left out and Same says what that is ("All
	// bounty-eligible, up to critical").
	Bounty, Severity bool
	Same             string
	// Hidden are the filter form's other choices.
	Hidden []param
}

// scopeLink is one choice on the Scope tab, with its count.
type scopeLink struct {
	Key, Label, URL string
	Count           int
	On              bool
}

// scopeSince is the Scope tab's "added in the last" windows, and their
// chips' order and labels.
var scopeSince = map[string]time.Duration{
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
	"90d": 90 * 24 * time.Hour,
}

var scopeSinceOrder = []string{"7d", "30d", "90d"}

var scopeSinceLabel = map[string]string{"7d": "Last 7 days", "30d": "Last 30 days", "90d": "Last 90 days"}

// scopeRowsAll bounds even the "show all" view: the export carries the rest.
const scopeRowsAll = 2000

// scopeRowsShown is how many rows the Scope tab shows before "show all".
const scopeRowsShown = 1000

// assetTypeOrder is the order of the Scope tab's type chips; types not
// listed follow, by name.
var assetTypeOrder = []string{"wildcard", "domain", "url", "api", "ip", "cidr", "android", "ios", "windows", "executable",
	"source_code", "hardware", "smart_contract", "ai_model", "other"}

// assetTypeLabels name asset types on the type chips.
var assetTypeLabels = map[string]string{
	"wildcard": "Wildcards", "domain": "Domains", "url": "URLs", "api": "APIs", "ip": "IPs", "cidr": "IP ranges",
	"android": "Android", "ios": "iOS", "windows": "Windows", "executable": "Executables", "source_code": "Source code",
	"hardware": "Hardware", "smart_contract": "Smart contracts", "ai_model": "AI models", "other": "Other",
}

// scopeView builds the Scope tab from the request's choices.
func (d *programData) scopeView(r *http.Request) scopeView {
	q := r.URL.Query()
	v := scopeView{
		Show:  oneOf(q.Get("show"), "in", "out", "removed"),
		Query: strings.TrimSpace(q.Get("q")),
		Order: oneOf(q.Get("order"), "", "added"),
		Since: oneOf(q.Get("since"), "", "7d", "30d", "90d"),
	}
	all := isTrue(q.Get("all"))
	lists := map[string][]store.EntryView{"in": d.InScope, "out": d.OutOfScope, "removed": d.Removed}
	for _, s := range []struct{ key, label string }{{"in", "In scope"}, {"out", "Out of scope"}, {"removed", "Removed"}} {
		v.Segments = append(v.Segments, scopeLink{Key: s.key, Label: s.label, Count: len(lists[s.key]), On: s.key == v.Show,
			URL: scopeURL(s.key, "", v.Query, v.Order, v.Since, false)})
	}
	list := slices.Clone(lists[v.Show])
	if v.Order == "added" && v.Show != "removed" {
		newestFirst(list, func(e store.EntryView) *time.Time { return e.AddedAt })
	}

	// Types, counted over the whole list so the chips don't move as the
	// search narrows it.
	counts := map[string]int{}
	for _, e := range list {
		counts[e.AssetType]++
	}
	if t := q.Get("type"); counts[t] > 0 {
		v.Type = t
	}
	types := make([]string, 0, len(counts))
	for t := range counts {
		types = append(types, t)
	}
	slices.SortFunc(types, func(a, b string) int {
		ia, ib := typeRank(a), typeRank(b)
		if ia != ib {
			return ia - ib
		}
		return strings.Compare(a, b)
	})
	if len(types) > 1 {
		v.Types = append(v.Types, scopeLink{Label: "All", Count: len(list), On: v.Type == "", URL: scopeURL(v.Show, "", v.Query, v.Order, v.Since, false)})
		for _, t := range types {
			v.Types = append(v.Types, scopeLink{Key: t, Label: assetTypeLabel(t), Count: counts[t], On: t == v.Type,
				URL: scopeURL(v.Show, t, v.Query, v.Order, v.Since, false)})
		}
	}
	// How much of the list is new, for the window chips. Counted over the
	// whole list (like the type chips), so they don't move as the search
	// narrows; removed entries are dated by their removal and keep none.
	now := time.Now()
	if v.Show != "removed" {
		counted := map[string]int{}
		for _, e := range list {
			if e.AddedAt == nil {
				continue
			}
			for key, span := range scopeSince {
				if now.Sub(*e.AddedAt) <= span {
					counted[key]++
				}
			}
		}
		v.SinceLinks = append(v.SinceLinks, scopeLink{Label: "All time", Count: len(list), On: v.Since == "",
			URL: scopeURL(v.Show, v.Type, v.Query, v.Order, "", false)})
		for _, w := range scopeSinceOrder {
			// Picking a window usually means "what is new": land on newest
			// first unless the list is already in a chosen order.
			order := v.Order
			if order == "" {
				order = "added"
			}
			v.SinceLinks = append(v.SinceLinks, scopeLink{Key: w, Label: scopeSinceLabel[w], Count: counted[w], On: v.Since == w,
				URL: scopeURL(v.Show, v.Type, v.Query, order, w, false)})
		}
	}

	v.Bounty, v.Severity, v.Same = sameColumns(list)

	needle := strings.ToLower(v.Query)
	for _, e := range list {
		if v.Type != "" && e.AssetType != v.Type {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(e.Identifier), needle) && !strings.Contains(strings.ToLower(e.Instruction), needle) {
			continue
		}
		if v.Since != "" && v.Show != "removed" {
			if e.AddedAt == nil || now.Sub(*e.AddedAt) > scopeSince[v.Since] {
				continue
			}
		}
		v.Rows = append(v.Rows, e)
	}
	v.Total = len(v.Rows)
	switch {
	case all && len(v.Rows) > scopeRowsAll:
		// "Show all" is still a page, and a program can carry thousands of
		// entries: the export is the tool for the rest.
		v.Rows, v.Limited, v.Truncated = v.Rows[:scopeRowsAll], true, true
	case !all && len(v.Rows) > scopeRowsShown:
		v.Rows, v.Limited = v.Rows[:scopeRowsShown], true
	}
	v.AllURL = scopeURL(v.Show, v.Type, v.Query, v.Order, v.Since, true)
	v.CopyURL = scopeURL(v.Show, v.Type, v.Query, v.Order, v.Since, false) + "&format=txt"
	v.ClearURL = scopeURL(v.Show, "", "", v.Order, "", false)
	other := map[string]string{"": "added", "added": ""}[v.Order]
	v.OrderURL = scopeURL(v.Show, v.Type, v.Query, other, v.Since, all)
	v.Hidden = []param{{Name: "tab", Value: "scope"}}
	for _, f := range []param{{"show", v.Show}, {"type", v.Type}, {"order", v.Order}, {"since", v.Since}} {
		if f.Value != "" && f.Value != "in" {
			v.Hidden = append(v.Hidden, f)
		}
	}
	return v
}

// typeRank is where an asset type's chip goes.
func typeRank(t string) int {
	if i := slices.Index(assetTypeOrder, t); i >= 0 {
		return i
	}
	return len(assetTypeOrder)
}

// assetTypeLabel names an asset type for a chip: "Source code".
func assetTypeLabel(t string) string {
	if l, ok := assetTypeLabels[t]; ok {
		return l
	}
	return upperFirst(strings.ReplaceAll(t, "_", " "))
}

// sameColumns says whether the Bounty and Severity columns tell the rows
// apart; for a column that doesn't, same says what every row has.
func sameColumns(list []store.EntryView) (bounty, severity bool, same string) {
	if len(list) == 0 {
		return true, true, ""
	}
	for _, e := range list[1:] {
		bounty = bounty || e.BountyEligible != list[0].BountyEligible
		severity = severity || e.MaxSeverity != list[0].MaxSeverity
	}
	var bits []string
	if !bounty {
		if list[0].BountyEligible {
			bits = append(bits, "all bounty-eligible")
		} else {
			bits = append(bits, "no bounties")
		}
	}
	if sev := list[0].MaxSeverity; !severity && sev != "" && sev != "none" {
		bits = append(bits, "up to "+sev)
	}
	return bounty, severity, upperFirst(strings.Join(bits, ", "))
}

// scopeURL links to the Scope tab with these choices.
func scopeURL(show, typ, query, order, since string, all bool) string {
	v := url.Values{"tab": {"scope"}}
	if show != "in" {
		v.Set("show", show)
	}
	if typ != "" {
		v.Set("type", typ)
	}
	if query != "" {
		v.Set("q", query)
	}
	if order != "" {
		v.Set("order", order)
	}
	if since != "" {
		v.Set("since", since)
	}
	if all {
		v.Set("all", "1")
	}
	return "?" + v.Encode()
}

// notePreview is the line a scope entry's note shows until it's opened,
// or "" when the note fits on one line as it is.
func notePreview(note string) string {
	note = strings.TrimSpace(note)
	first := firstLine(note)
	if first == note && len(note) <= 90 {
		return ""
	}
	return first
}

// noteRest is the rest of a note after the line notePreview shows.
func noteRest(note string) string {
	lines := strings.Split(strings.TrimSpace(note), "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			return strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
		}
	}
	return ""
}

// expansion is what a scope entry naming several targets covers
// ("*.example.{com,co.uk}", "a.example.com, b.example.com"): the targets
// stored for it, as Search and Targets list them, the first expansionShown
// in Text and all of them in Title. Nothing for an entry naming one.
type expansion struct{ Text, Title string }

const expansionShown = 12

func expandsTo(e store.EntryView) *expansion {
	if len(e.Targets) < 2 {
		return nil
	}
	values := make([]string, len(e.Targets))
	for i, t := range e.Targets {
		values[i] = t.Value
	}
	x := &expansion{Text: strings.Join(values[:min(len(values), expansionShown)], " · "), Title: strings.Join(values, "\n")}
	if n := len(values) - expansionShown; n > 0 {
		x.Text += " · +" + strconv.Itoa(n) + " more"
	}
	return x
}

// writeScopeText answers the Scope tab's copy link: one identifier per line,
// as the view shows them (every row, past the page's limit).
func (s *server) writeScopeText(w http.ResponseWriter, v scopeView) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	for _, e := range v.Rows {
		_, _ = fmt.Fprintln(w, e.Identifier)
	}
}
