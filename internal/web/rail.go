package web

import (
	"net/url"
	"slices"
	"strings"
)

// The Programs, Targets, Changes and Disclosed pages share a filter rail
// (templates/partials/rail.html): a search box, then groups of choices,
// each a link that turns it on or off, with how many rows it would list.
// The active ones show as pills above the list. Below 1600px wide, where
// the rail sits above the list, its choices fold away behind a Filters
// button beside the search box (app.js; shown without scripts).

// railView is what the rail partial shows.
type railView struct {
	Path        string // the page, which the search form submits to
	Search      string // the search box's current value
	Placeholder string
	Label       string  // the search box's accessible name
	Hidden      []param // the page's other filters, kept when searching
	Groups      []railGroup
	// Views, if set, puts the page's saved views (myviews.go) at the top.
	Views *myViews
	// Exclude adds a field for words to leave out (?not=, exclude.go)
	// under the search box, Not its value.
	Exclude bool
	Not     string
}

// Active is how many filters are on (not views, nor a list's default), for
// the button that shows the folded rail on narrower screens.
func (v railView) Active() int {
	n := 0
	for _, g := range v.Groups {
		for _, o := range g.Options {
			if o.On && !o.View && !o.Default {
				n++
			}
		}
	}
	return n
}

// railGroup is one group of choices in a filter rail.
type railGroup struct {
	Title   string
	Options []railOption
	Form    *railForm // a value typed in, below the choices
	Long    bool      // long labels: one line each, cut short (whole in the tooltip)
	// Watch marks the watchlist's group (watch.go): hidden unless On, for
	// watch.js to show.
	Watch, On bool
}

// railForm is a rail group's field for a value of the user's own: a
// GET form keeping the page's other filters.
type railForm struct {
	Path, Name, Value, Placeholder, Label, Title string
	Hidden                                       []param
}

// railOption is one choice: a link that turns it on (replacing the
// group's other choice) or, when On, off again.
type railOption struct {
	Label    string
	Title    string
	Platform string // shows the platform's tag
	Count    int
	Counted  bool // Count is known; some choices aren't counted
	On       bool
	URL      string
	Default  bool // the list's default: on without a filter, so never a pill
	View     bool // a saved view (a round box), not a filter
	Watch    bool // links to the browser's watchlist (watch.js sets the link)
	Noted    bool // the programs with notes (?noted=), kept by the server since notes left the browser
}

// pill is an active filter, with a link that drops it. Search marks the
// search's: only its own link drops the search, never clearing filters.
type pill struct {
	Label, URL string
	Search     bool
}

// hasFilters says whether pills has any besides the search's, for the
// links that clear filters (and keep the search).
func hasFilters(pills []pill) bool {
	return slices.ContainsFunc(pills, func(p pill) bool { return !p.Search })
}

// param is a query parameter, for hidden form fields.
type param struct{ Name, Value string }

// listQuery is a list page's current query, for links that change one
// filter and go back to the first page.
type listQuery struct {
	path  string
	query url.Values
}

// with is the current query with key set to value (dropped if value is "").
func (l listQuery) with(key, value string) string {
	q := url.Values{}
	for k, v := range l.query {
		if k != "page" && k != key && len(v) > 0 && v[0] != "" {
			q[k] = v
		}
	}
	if value != "" {
		q.Set(key, value)
	}
	if len(q) == 0 {
		return l.path
	}
	return l.path + "?" + q.Encode()
}

// without is the query without keys, for choices that drop other filters.
func (l listQuery) without(keys ...string) listQuery {
	q := url.Values{}
	for k, v := range l.query {
		if !slices.Contains(keys, k) {
			q[k] = v
		}
	}
	return listQuery{l.path, q}
}

// option is a choice setting key to value, on when the query has it.
func (l listQuery) option(label, title, key, value string, count int, counted bool) railOption {
	return l.choice(label, title, key, value, l.query.Get(key) == value, count, counted)
}

// choice is a choice setting key to value, on as given (for parameters
// that take more than one spelling); turning it off drops key.
func (l listQuery) choice(label, title, key, value string, on bool, count int, counted bool) railOption {
	link := l.with(key, value)
	if on {
		link = l.with(key, "")
	}
	return railOption{Label: label, Title: title, Count: count, Counted: counted, On: on, URL: link}
}

// fallback is a group's default choice: on as given, and choosing it drops
// key, going back to the default.
func (l listQuery) fallback(label, title, key string, on bool, count int, counted bool) railOption {
	return railOption{Label: label, Title: title, Count: count, Counted: counted, On: on, URL: l.with(key, ""), Default: true}
}

// hidden are the current query's parameters other than skip, for forms
// that change one of them.
func (l listQuery) hidden(skip ...string) []param {
	var out []param
	for k, vs := range l.query {
		if k == "page" || slices.Contains(skip, k) {
			continue
		}
		for _, v := range vs {
			if v != "" {
				out = append(out, param{k, v})
			}
		}
	}
	slices.SortFunc(out, func(a, b param) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// pills are the filters groups have on (not defaults or views), after the
// search, if any.
func (l listQuery) pills(groups []railGroup) []pill {
	var out []pill
	if q := strings.TrimSpace(l.query.Get("q")); q != "" {
		out = append(out, pill{Label: "“" + q + "”", URL: l.with("q", ""), Search: true})
	}
	for _, g := range groups {
		for _, o := range g.Options {
			if o.On && !o.Default && !o.View {
				out = append(out, pill{Label: o.Label, URL: o.URL})
			}
		}
	}
	return out
}

// keeping is path with only the given parameters of the current query, for
// "Clear filters" links that keep, say, the sort (and always the search).
func (l listQuery) keeping(keys ...string) string {
	q := url.Values{}
	for _, k := range append(keys[:len(keys):len(keys)], "q") {
		if v := l.query.Get(k); v != "" {
			q.Set(k, v)
		}
	}
	if len(q) == 0 {
		return l.path
	}
	return l.path + "?" + q.Encode()
}
