package web

import (
	"net/url"
	"slices"
	"strings"
)

// The Reports list's built-in views, saved views and column picker, as
// the Programs list has them.

// reportViews are the rail's built-in views, as queries.
var reportViews = []struct{ Label, Title, Query string }{
	{"Needs you", "The program is waiting on your reply", "status=you"},
	{"Gone quiet", "Not decided yet, and no word from the program for a while (Settings › Reports sets how long)", "status=stale"},
	{"Awaiting bounty", "Valid, with nothing paid yet", "status=unpaid"},
	{"Newest submitted", "The latest you submitted first", "sort=submitted"},
	{"Biggest bounties", "The best paid first", "sort=bounty&status=paid"},
}

// reportViewKeeps are what views keep: the search, the platform and a
// program page's link.
var reportViewKeeps = []string{"q", "platform", "program"}

// reportViewKeys are the query keys saved views keep.
var reportViewKeys = []string{"status", "platform", "state", "severity", "year", "q", "program", "sort", "rev"}

// reportViewGroup is the rail's Views: the one matching the query (what
// views keep aside) is on, and turning it off clears it.
func reportViewGroup(q url.Values) railGroup {
	kept, cur := url.Values{}, url.Values{}
	for k, v := range q {
		switch {
		case len(v) == 0 || strings.TrimSpace(v[0]) == "" || k == "page":
		case slices.Contains(reportViewKeeps, k):
			kept.Set(k, strings.TrimSpace(v[0]))
		default:
			cur[k] = v
		}
	}
	link := func(view string) string {
		u, _ := url.ParseQuery(view) // the views' own, well formed
		for k := range kept {
			u.Set(k, kept.Get(k))
		}
		if len(u) == 0 {
			return "/reports"
		}
		return "/reports?" + u.Encode()
	}
	g := railGroup{Title: "Views"}
	for _, v := range reportViews {
		vq, _ := url.ParseQuery(v.Query)
		o := railOption{Label: v.Label, Title: v.Title, URL: link(v.Query), On: vq.Encode() == cur.Encode(), View: true}
		if o.On {
			o.URL = link("")
		}
		g.Options = append(g.Options, o)
	}
	return g
}

// reportColumns are the Reports list's columns Customize shows, hides and
// reorders; Closed and Triaged are hidden until chosen. The program comes
// first, as on every list.
var reportColumns = columnPicker{Name: "reports", Label: "Customize", Icon: "grid", Fixed: "Program", Columns: []tableColumn{
	{Key: "report", Label: "Report"}, {Key: "sev", Label: "Severity"}, {Key: "state", Label: "State"},
	{Key: "bounty", Label: "Earned"}, {Key: "submitted", Label: "Submitted"}, {Key: "activity", Label: "Activity"},
	{Key: "triaged", Label: "Triaged"}, {Key: "closed", Label: "Closed"},
}}

// Columns is the list's column picker.
func (reportsView) Columns() columnPicker { return reportColumns }

// CSVURL is the list's CSV export, with its filters and sort.
func (v reportsView) CSVURL() string {
	keep := url.Values{}
	for k, val := range v.Query.query {
		if k != "page" {
			keep[k] = val
		}
	}
	if len(keep) == 0 {
		return "/reports.csv"
	}
	return "/reports.csv?" + keep.Encode()
}
