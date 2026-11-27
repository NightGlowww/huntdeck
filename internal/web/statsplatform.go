package web

import (
	"cmp"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// A platform's stats page (/stats/{platform}): the Stats page's tiles for
// that platform, then its open programs, each with the same numbers and
// sparklines, sortable and filterable.

// statsPlatformPerPage is how many programs a platform's stats page lists.
const statsPlatformPerPage = 50

// statsPlatformSorts are the orderings a platform's stats page offers; the
// first the page's columns allow is the default.
var statsPlatformSorts = []string{"paid", "received", "added", "valid", "bounties", "resolve", "name"}

// statsPlatformData is a platform's stats page.
type statsPlatformData struct {
	Platform platformStats   // the tiles
	Rows     []platformStats // this page's programs
	// Columns the platform has numbers for: "received", "valid",
	// "bounties", "paid", "resolve".
	Columns    map[string]bool
	Sort       string
	Reverse    bool
	Query      string
	BountyOnly bool
	Pager      pager
	Vis        visToggle // All, Public or Private
	H1Stats    *researcherStatsView
	// days are the programs' daily numbers by column, then program, for
	// show to draw the rows' 90-day lines from.
	days map[string]map[int64][]store.ProgramDay
}

// show keeps rows from to to (as far as there are any), with their 90-day
// lines: the rest are only sorted.
func (d *statsPlatformData) show(from, to int) {
	d.Rows = d.Rows[min(from, len(d.Rows)):min(to, len(d.Rows))]
	for i := range d.Rows {
		own := map[string][]store.ProgramDay{}
		for col, by := range d.days {
			own[col] = by[d.Rows[i].Item.ID]
		}
		d.Rows[i].addDayLines(own)
	}
}

// SortURL is this page sorted by key, back on the first page; the current
// sort's turns it around. SortClass is its header's class.
func (d statsPlatformData) SortURL(key string) string {
	q := url.Values{"sort": {key}}
	if key == d.Sort && !d.Reverse {
		q.Set("rev", "1")
	}
	if d.Query != "" {
		q.Set("q", d.Query)
	}
	if d.Vis.Value != "" {
		q.Set("visibility", d.Vis.Value)
	}
	if d.BountyOnly {
		q.Set("bounty", "1")
	}
	return "/stats/" + d.Platform.Platform + "?" + q.Encode()
}

func (d statsPlatformData) SortClass(key string) string { return sortClass(d.Sort == key, d.Reverse) }

func (s *server) statsPlatform(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := canonicalPlatform(r.PathValue("platform"))
	vis := statsVisibility(r)
	in, err := s.statsInputs(ctx, vis)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	o, ok := s.statsOverviewFor(in, name, vis)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "No such platform.")
		return
	}
	series, err := s.programSeries(ctx, vis)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var items []store.ProgramListItem
	for _, it := range in.Items {
		if it.Platform == name {
			items = append(items, it)
		}
	}
	days := map[string][]store.ProgramDay{}
	for col, by := range in.Days {
		days[col] = by[name]
	}
	q := r.URL.Query()
	d := buildStatsPlatform(o, items, series, days, in.Start, strings.TrimSpace(q.Get("q")), isTrue(q.Get("bounty")),
		q.Get("sort"), isTrue(q.Get("rev")))
	if d.Vis, err = s.statsToggle(ctx, r, name); err != nil {
		s.serverError(w, r, err)
		return
	}
	d.Pager = newPager(r, statsPlatformPerPage, len(d.Rows))
	d.show(d.Pager.Offset(), d.Pager.Offset()+statsPlatformPerPage)
	if name == "hackerone" {
		d.H1Stats = s.h1ResearcherStats(ctx)
	}
	s.render(w, r, http.StatusOK, "statsplatform", pageData{Title: platformLabel(name) + " stats", Nav: "stats", Data: d})
}

// buildStatsPlatform lays out a platform's stats page: its tiles from its
// overview, its open programs and their daily numbers; then each program
// matching query and bountyOnly, sorted by sortKey (reversed by rev).
func buildStatsPlatform(o store.PlatformOverview, items []store.ProgramListItem, series map[int64]store.PlatformOverview,
	days map[string][]store.ProgramDay, start time.Time, query string, bountyOnly bool, sortKey string, rev bool) statsPlatformData {
	months := make([]time.Time, overviewMonths)
	for i := range months {
		months[i] = start.AddDate(0, i, 0)
	}
	month := months[overviewMonths-2].Format("January")
	d := statsPlatformData{Query: query, BountyOnly: bountyOnly, Reverse: rev}
	d.Platform = platformStatsFor(o, months, items, days, true)
	d.Platform.Month = month
	d.Columns = map[string]bool{"received": d.Platform.ReceivedCell() != nil, "valid": d.Platform.ValidCell() != nil,
		"bounties": d.Platform.BountiesCell() != nil, "paid": d.Platform.PaidCell() != nil, "resolve": d.Platform.ResolveCell() != nil}

	byProgram := map[string]map[int64][]store.ProgramDay{}
	for col, list := range days {
		byProgram[col] = map[int64][]store.ProgramDay{}
		for _, pd := range list {
			byProgram[col][pd.ProgramID] = append(byProgram[col][pd.ProgramID], pd)
		}
	}
	d.days = byProgram
	q := strings.ToLower(query)
	for i := range items {
		it := &items[i]
		if bountyOnly && !it.OffersBounty {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(it.Name), q) && !strings.Contains(strings.ToLower(it.Handle), q) {
			continue
		}
		po, ok := series[it.ID]
		if !ok {
			po = store.PlatformOverview{Platform: it.Platform, Months: make([]store.PlatformMonth, overviewMonths)}
		}
		own := map[string][]store.ProgramDay{}
		for col, by := range byProgram {
			own[col] = by[it.ID]
		}
		row := platformStatsFor(po, months, []store.ProgramListItem{*it}, own, false)
		if d.Platform.Resolve != nil { // decision times only where the platform has no resolutions at all
			row.Decision, row.DecisionNow, row.DecisionSince, row.decisionDays = nil, nil, nil, nil
		}
		row.Item, row.Month = it, month
		d.Rows = append(d.Rows, row)
	}

	d.Sort = ""
	for _, k := range statsPlatformSorts {
		if k == sortKey && (d.Columns[k] || k == "added" || k == "name") {
			d.Sort = k
		}
	}
	if d.Sort == "" {
		d.Sort = "added"
		for _, k := range []string{"paid", "received", "valid"} {
			if d.Columns[k] {
				d.Sort = k
				break
			}
		}
	}
	lead := d.Platform.BountiesCurrency
	if d.Sort == "paid" && len(d.Platform.Paid) > 0 {
		lead = d.Platform.Paid[0].Currency
	}
	sortStatsRows(d.Rows, d.Sort, rev, lead)
	return d
}

// sortStatsRows sorts programs by key: biggest first (quickest first for
// "resolve", A to Z for "name"), turned around by rev; programs without
// the number go last either way. Amounts in different currencies can't be
// compared, so by "paid" or "bounties" the programs paying in lead (the
// currency most is paid in, the platform's line) come first, then each
// other currency's, each sorted within itself.
func sortStatsRows(rows []platformStats, key string, rev bool, lead string) {
	currency := func(r platformStats) string {
		switch {
		case key == "paid" && len(r.Paid) > 0:
			return r.Paid[0].Currency
		case key == "bounties":
			return r.BountiesCurrency
		}
		return ""
	}
	group := func(r platformStats) (int, string) {
		c := currency(r)
		if c == lead {
			return 0, ""
		}
		return 1, c
	}
	value := func(r platformStats) (float64, bool) {
		switch key {
		case "paid":
			if len(r.Paid) == 0 {
				return 0, false
			}
			return r.Paid[0].Amount, true
		case "received":
			if r.ReceivedNow == nil {
				return 0, false
			}
			return float64(*r.ReceivedNow), true
		case "added":
			return float64(r.ScopeLast), true
		case "valid":
			return float64(r.ValidLast), r.Valid != nil
		case "bounties":
			return r.BountiesLast, r.Bounties != nil
		case "resolve":
			switch {
			case r.ResolveLast != nil:
				return -*r.ResolveLast, true // quickest first
			case r.Resolve == nil && r.DecisionNow != nil:
				return -*r.DecisionNow, true
			}
			return 0, false
		}
		return 0, false
	}
	slices.SortStableFunc(rows, func(a, b platformStats) int {
		if key == "name" {
			c := cmp.Compare(strings.ToLower(a.Item.Name), strings.ToLower(b.Item.Name))
			if rev {
				c = -c
			}
			return c
		}
		va, oka := value(a)
		vb, okb := value(b)
		switch {
		case oka != okb:
			if oka {
				return -1
			}
			return 1
		}
		ga, ca := group(a)
		gb, cb := group(b)
		if c := cmp.Or(cmp.Compare(ga, gb), cmp.Compare(ca, cb)); c != 0 && oka {
			return c
		}
		switch {
		case va != vb:
			c := cmp.Compare(vb, va)
			if rev {
				c = -c
			}
			return c
		}
		return cmp.Compare(strings.ToLower(a.Item.Name), strings.ToLower(b.Item.Name))
	})
}
