package web

import (
	"cmp"
	"context"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/store"
)

// The Stats page: every platform at a glance. Headline tiles for all of
// them, then a row per platform (and one for all) with each number and a
// sparkline of it: a year by month, or every day saved for the 90-day
// numbers. Comparing programs is the Programs list's job.

// overviewMonths is how many months the page's month lines cover.
const overviewMonths = 12

// statsDays are the daily numbers the Stats pages chart.
var statsDays = []string{store.DayReports90d, store.DayPaid90d, store.DayDecisionHours}

// statsDayMin is the shortest history the page draws a daily line for:
// two days saved (saves drift through the day, so two can be under 24
// hours apart). Lines start with what there is rather than waiting.
const statsDayMin = 12 * time.Hour

// statsData is the Stats page.
type statsData struct {
	Rows []platformStats // a row per platform, platform order
	All  platformStats   // every platform together
	// Month names the last finished month, the one the per-month numbers
	// give: "October".
	Month string
	Vis   visToggle // All, Public or Private
	// AllLeftOut names the platforms All's valid reports, bounties and days
	// to resolve leave out, and since when they're counted: "YesWeHack
	// (counted since Oct 2026)"; "" for none.
	AllLeftOut string
	// OwnLeftOut is set when there are programs added by hand, which
	// aren't on a platform to count.
	OwnLeftOut bool
}

// platformStats is one platform (or all of them, Platform "") on the Stats
// page. A nil chart is a number the platform doesn't publish, or not for
// long enough yet.
type platformStats struct {
	// VisQuery is the Stats page's visibility for the row's link to the
	// platform's page ("?visibility=private"), "" for all.
	VisQuery string

	Month string // the last finished month: \"October\"
	// Item is the program, on a platform's stats page; nil for a platform.
	Item                 *store.ProgramListItem
	Platform             string
	Open, Paying, Assets int
	OpenLine, AssetsLine *lineChart // at each month's end; platforms only
	PayingLine, FreeLine *lineChart // open programs paying bounties, and not
	// The Stats page's tiles beside the totals; platforms only.
	Wildcards                        int // in scope now
	WildcardsLine                    *lineChart
	ScopePrograms, Disclosed         int // last month's
	ScopeProgramsLine, DisclosedLine *lineChart
	Added30, Removed30               int
	ScopeAdded                       lineChart
	ScopeLast                        int // added in the last finished month
	Launched                         *lineChart
	LaunchedLast                     int // launched in the last finished month
	Received                         *lineChart
	ReceivedNow                      *int
	ReceivedEstimated                bool       // some counts are estimates (store.ProgramListItem.Reports90dIsEstimate)
	ReceivedSince, PaidSince         *time.Time // first day saved, while there isn't a line yet
	Valid, Bounties, Resolve         *lineChart
	ValidKind                        string // "resolved", "accepted" or both: "valid"
	ValidLast                        int
	// ValidFrom is the first day counted when that's in the last finished
	// month ("Oct 28"; ValidPart) or after it.
	ValidFrom string
	ValidPart bool
	ValidEst  bool // ValidLast is partly estimated (store.PlatformMonth.Estimated)
	// Decision is the median of programs' average days to the company's
	// decision on a report over the last 90 days, by day, where the
	// platform gives that and no resolutions (Intigriti).
	Decision         *lineChart
	DecisionNow      *float64
	DecisionSince    *time.Time // first day saved, while there isn't a line yet
	BountiesLast     float64
	BountiesCurrency string     // of the chart and BountiesLast: the currency most is paid in
	BountiesAlso     []moneySum // last month's bounties in other currencies
	BountiesEst      bool       // some are estimated (bountyEstimateFrom)
	ResolveLast      *float64
	Paid             []moneySum // paid in 90 days by currency, largest first
	PaidLine         *lineChart // programs paying in Paid[0]'s currency
	PaidPrograms     int
	PaidEstimated    bool // some amounts are estimates (store.ProgramListItem.Paid90dIsEstimate)
	ReceivedPrograms int

	// The numbers behind the charts, for the API (api_statspage.go).
	overview                             store.PlatformOverview
	months                               []time.Time
	receivedDays, paidDays, decisionDays []store.DayValue
}

// Free counts open programs that pay no bounties.
func (r platformStats) Free() int { return r.Open - r.Paying }

// moneySum is an amount in one currency.
type moneySum struct {
	Currency string
	Amount   float64
}

func (s *server) stats(w http.ResponseWriter, r *http.Request) {
	t0 := time.Now()
	in, err := s.statsInputs(r.Context(), statsVisibility(r))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	tInputs := time.Since(t0)
	// Laying the page out walks every program and every day saved: the same
	// work for the same data, so it is memoised with the inputs it reads.
	d, err := store.Memo(r.Context(), s.Store, "stats-page:"+statsVisibility(r)+":"+in.Start.Format(time.DateOnly),
		func(context.Context) (statsData, error) {
			return buildStats(in.Overviews, s.platformNames(), in.Items, in.Days, in.Start), nil
		})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// The rows are the request's own from here: each carries this view's link.
	d.Rows = slices.Clone(d.Rows)
	tBuild := time.Since(t0)
	if d.Vis, err = s.statsToggle(r.Context(), r, ""); err != nil {
		s.serverError(w, r, err)
		return
	}
	for i := range d.Rows {
		d.Rows[i].VisQuery = d.Vis.Query() // its link keeps the choice
	}
	if d.OwnLeftOut, err = s.Store.HasPrograms(r.Context(), store.Independent); err != nil {
		s.serverError(w, r, err)
		return
	}
	tData := time.Since(t0)
	s.render(w, r, http.StatusOK, "stats", pageData{Title: "Stats", Nav: "stats", Data: d})
	s.Log.Debug("stats served", "inputs", tInputs.Round(time.Millisecond), "build", (tBuild - tInputs).Round(time.Millisecond),
		"rest", (tData - tBuild).Round(time.Millisecond), "render", (time.Since(t0) - tData).Round(time.Millisecond))
}

// buildStats lays out the Stats page from each platform's overview (in
// order, the configured platforms' order; any other after), its open
// programs as the Programs list has them (for the 90-day numbers, so the
// two agree) and its programs' daily numbers by column.
func buildStats(overviews []store.PlatformOverview, order []string, items []store.ProgramListItem,
	days map[string]map[string][]store.ProgramDay, start time.Time) statsData {
	rank := func(p string) int {
		if i := slices.Index(order, p); i >= 0 {
			return i
		}
		return len(order)
	}
	overviews = slices.Clone(overviews)
	slices.SortStableFunc(overviews, func(a, b store.PlatformOverview) int {
		return cmp.Or(cmp.Compare(rank(a.Platform), rank(b.Platform)), cmp.Compare(a.Platform, b.Platform))
	})
	months := make([]time.Time, overviewMonths)
	for i := range months {
		months[i] = start.AddDate(0, i, 0)
	}
	d := statsData{Month: months[overviewMonths-2].Format("January")}
	byPlatform := map[string][]store.ProgramListItem{}
	var platformItems []store.ProgramListItem // programs added by hand aren't on a platform to compare
	for _, it := range items {
		if it.Platform == store.Independent {
			continue
		}
		byPlatform[it.Platform] = append(byPlatform[it.Platform], it)
		platformItems = append(platformItems, it)
	}
	items = platformItems
	allDays := map[string][]store.ProgramDay{}
	for _, o := range overviews {
		own := map[string][]store.ProgramDay{}
		for col, by := range days {
			own[col] = by[o.Platform]
		}
		d.Rows = append(d.Rows, platformStatsFor(o, months, byPlatform[o.Platform], own, true))
		for _, col := range []string{store.DayReports90d, store.DayPaid90d} { // decision times aren't resolutions: not added up
			allDays[col] = append(allDays[col], own[col]...)
		}
	}
	all := store.PlatformOverview{Months: make([]store.PlatformMonth, overviewMonths)}
	kinds := map[string]bool{}
	var leftOut []string
	for _, o := range overviews {
		all.Open, all.Paying, all.Assets = all.Open+o.Open, all.Paying+o.Paying, all.Assets+o.Assets
		all.Added30, all.Removed30 = all.Added30+o.Added30, all.Removed30+o.Removed30
		all.Launches = all.Launches || o.Launches
		// A platform whose valid reports are counted from within the last
		// half year would cut every platform's line short: it's left out
		// until then.
		valid := o.ValidKind != "" && (o.ValidSince == nil || !o.ValidSince.After(months[overviewMonths/2]))
		if o.ValidKind != "" && !valid {
			leftOut = append(leftOut, platformLabel(o.Platform)+" (counted since "+o.ValidSince.Format("Jan 2006")+")")
		}
		if valid {
			kinds[o.ValidKind] = true
			all.ValidKind = o.ValidKind
			// Every platform's counts are complete from the latest start.
			if o.ValidSince != nil && (all.ValidSince == nil || o.ValidSince.After(*all.ValidSince)) {
				all.ValidSince = o.ValidSince
			}
		}
		for i, m := range o.Months {
			a := &all.Months[i]
			a.ScopeAdded += m.ScopeAdded
			a.Open += m.Open
			a.Paying += m.Paying
			a.Wildcards += m.Wildcards
			a.ScopePrograms += m.ScopePrograms
			a.Disclosed += m.Disclosed
			a.Assets += m.Assets
			a.Launched += m.Launched
			if valid {
				a.Valid += m.Valid
				a.Estimated = a.Estimated || m.Estimated
				a.Bounties += m.Bounties
				for c, v := range m.BountiesOther {
					a.AddBounty(c, v)
				}
				a.BountiesEstimated = a.BountiesEstimated || m.BountiesEstimated
				a.ResolveDays = append(a.ResolveDays, m.ResolveDays...)
			}
		}
	}
	if len(kinds) > 1 {
		all.ValidKind = "valid"
	}
	d.All = platformStatsFor(all, months, items, allDays, true)
	d.AllLeftOut = strings.Join(leftOut, ", ")
	for i := range d.Rows {
		d.Rows[i].Month = d.Month
	}
	d.All.Month = d.Month
	return d
}

// platformStatsFor builds one row from a platform's overview (or all of
// them), its open programs and its programs' daily numbers by column
// (statsDays); with its 90-day lines unless !lines (addDayLines).
func platformStatsFor(o store.PlatformOverview, months []time.Time, items []store.ProgramListItem, daily map[string][]store.ProgramDay,
	lines bool) platformStats {
	row := platformStats{Platform: o.Platform, Open: o.Open, Paying: o.Paying, Assets: o.Assets, Added30: o.Added30, Removed30: o.Removed30,
		ValidKind: o.ValidKind, overview: o, months: months}
	n := len(months)
	at := func(i int) string { return months[i].Format("Jan 2006") }
	series := func(f func(i int, m store.PlatformMonth) (float64, bool), say func(float64) string) []chartValue {
		out := make([]chartValue, n)
		for i, m := range o.Months {
			v, known := f(i, m)
			out[i] = chartValue{V: v, Tip: chartTip(at(i), say(v)), Known: known}
		}
		return out
	}
	count := func(what string) func(float64) string {
		return func(v float64) string { return formatCount(v, "") + " " + what }
	}
	if slices.ContainsFunc(o.Months, func(m store.PlatformMonth) bool { return m.Open > 0 }) {
		end := func(i int) string {
			if i == n-1 {
				return "Now"
			}
			return "End of " + at(i)
		}
		level := func(f func(store.PlatformMonth) int, what string) []chartValue {
			out := make([]chartValue, n)
			for i, m := range o.Months {
				out[i] = chartValue{V: float64(f(m)), Tip: chartTip(end(i), num(f(m))+" "+what), Known: true}
			}
			return out
		}
		op := buildMonthChart(months, level(func(m store.PlatformMonth) int { return m.Open }, "open"),
			chartOpts{Label: "Open programs at each month's end", Floor: true})
		paying := buildMonthChart(months, level(func(m store.PlatformMonth) int { return m.Paying }, "paying bounties"),
			chartOpts{Label: "Open programs paying bounties at each month's end", Floor: true})
		free := buildMonthChart(months, level(func(m store.PlatformMonth) int { return m.Open - m.Paying }, "free"),
			chartOpts{Label: "Open programs paying no bounties at each month's end", Floor: true})
		row.PayingLine, row.FreeLine = &paying, &free
		wild := buildMonthChart(months, level(func(m store.PlatformMonth) int { return m.Wildcards }, "wildcards in scope"),
			chartOpts{Label: "In-scope wildcards of today's open programs at each month's end", Floor: true})
		row.WildcardsLine, row.Wildcards = &wild, o.Months[n-1].Wildcards
		scoped := buildMonthChart(months, series(func(_ int, m store.PlatformMonth) (float64, bool) { return float64(m.ScopePrograms), true },
			count("programs added scope")), chartOpts{Label: "Programs that added scope, per month", Open: 1})
		row.ScopeProgramsLine, row.ScopePrograms = &scoped, o.Months[n-2].ScopePrograms
		if slices.ContainsFunc(o.Months, func(m store.PlatformMonth) bool { return m.Disclosed > 0 }) {
			d := buildMonthChart(months, series(func(_ int, m store.PlatformMonth) (float64, bool) { return float64(m.Disclosed), true },
				count("disclosed")), chartOpts{Label: "Reports disclosed per month", Open: 1})
			row.DisclosedLine, row.Disclosed = &d, o.Months[n-2].Disclosed
		}
		as := buildMonthChart(months, level(func(m store.PlatformMonth) int { return m.Assets }, "in scope"),
			chartOpts{Label: "In-scope assets of today's open programs at each month's end", Floor: true})
		row.OpenLine, row.AssetsLine = &op, &as
	}
	row.ScopeLast = o.Months[n-2].ScopeAdded
	row.ScopeAdded = buildMonthChart(months, series(func(_ int, m store.PlatformMonth) (float64, bool) { return float64(m.ScopeAdded), true },
		count("added to scope")), chartOpts{Label: "Assets added to scope per month", Open: 1})
	if o.Launches {
		c := buildMonthChart(months, series(func(_ int, m store.PlatformMonth) (float64, bool) { return float64(m.Launched), true },
			count("launched")), chartOpts{Label: "Programs launched per month", Open: 1})
		row.Launched, row.LaunchedLast = &c, o.Months[n-2].Launched
	}
	if o.ValidKind != "" {
		known := func(i int) bool { k, _ := monthKnown(months[i], o.ValidSince); return o.ValidSince == nil || k }
		valid := series(func(i int, m store.PlatformMonth) (float64, bool) { return float64(m.Valid), known(i) }, count(o.ValidKind))
		for i, m := range o.Months {
			switch _, from := monthKnown(months[i], o.ValidSince); {
			case m.Estimated:
				valid[i].Tip = chartTip(at(i), "≈"+count(o.ValidKind)(valid[i].V)+", partly estimated"+validEstimateFrom(o.Platform))
			case from != "":
				valid[i].Tip += " (counted from " + from + ")"
			}
		}
		row.ValidEst = o.Months[n-2].Estimated
		c := buildMonthChart(months, valid, chartOpts{Label: "Valid reports per month", Open: 1})
		row.Valid, row.ValidLast = &c, o.Months[n-2].Valid
		if o.ValidSince != nil {
			if !known(n - 2) { // counting started this month: last month has no number
				row.ValidFrom = o.ValidSince.UTC().Format("Jan 2")
			} else if _, row.ValidFrom = monthKnown(months[n-2], o.ValidSince); row.ValidFrom != "" {
				row.ValidPart = true
			}
		}
		if curs := bountyCurrencies(o.Months); len(curs) > 0 {
			cur, word := curs[0], bountyWord(o.Platform)
			bounties := series(func(i int, m store.PlatformMonth) (float64, bool) { return bountiesIn(m, cur), known(i) },
				func(v float64) string { return humanize.MoneyFull(v, cur) + word })
			for i, m := range o.Months {
				if m.BountiesEstimated && bounties[i].Known {
					row.BountiesEst = true
					bounties[i].Tip = chartTip(at(i), "≈"+humanize.Money(bountiesIn(m, cur), cur)+word+", "+bountyEstimateFrom(o.Platform))
				}
			}
			label := "Bounties" + word + " per month"
			if cur != "USD" || len(curs) > 1 {
				label += ", " + cur + " programs"
			}
			b := buildMonthChart(months, bounties, chartOpts{Label: label, Open: 1, Format: moneyAxis(cur)})
			row.Bounties, row.BountiesLast, row.BountiesCurrency = &b, bountiesIn(o.Months[n-2], cur), cur
			for _, c := range curs[1:] {
				if v := bountiesIn(o.Months[n-2], c); v > 0 {
					row.BountiesAlso = append(row.BountiesAlso, moneySum{c, v})
				}
			}
		}
		resolve := make([]chartValue, n)
		timed := false
		for i, m := range o.Months {
			resolve[i] = chartValue{Tip: chartTip(at(i), "none resolved")}
			if md := medianOf(m.ResolveDays); md != nil {
				resolve[i] = chartValue{V: *md, Tip: chartTip(at(i), humanize.Days(*md)+" median to resolve"), Known: true}
				timed = true
			}
		}
		if timed {
			c := buildMonthChart(months, resolve, chartOpts{Label: "Median days to resolve, by the month resolved", Open: 1,
				Format: humanize.Days, LowerBetter: true})
			row.Resolve, row.ResolveLast = &c, medianOf(o.Months[n-2].ResolveDays)
		}
	}

	// The 90-day numbers, as the Programs list has them.
	sums := sumNinetyDays(items)
	row.ReceivedPrograms, row.ReceivedEstimated, row.Paid, row.PaidPrograms, row.PaidEstimated =
		sums.ReceivedPrograms, sums.ReceivedEstimated, sums.Paid, sums.PaidPrograms, sums.PaidEstimated
	if sums.ReceivedPrograms > 0 {
		row.ReceivedNow = &sums.Received
	}
	if row.Resolve == nil {
		row.Decision, row.DecisionNow, row.DecisionSince, row.decisionDays = decisionChart(daily[store.DayDecisionHours])
	}
	if lines {
		row.addDayLines(daily)
	}
	return row
}

// addDayLines adds a row's 90-day lines, received and paid, from its
// programs' daily numbers: the slowest part of a row, so a platform's
// stats page draws them only for the programs it shows.
func (row *platformStats) addDayLines(daily map[string][]store.ProgramDay) {
	recv := 0
	if row.ReceivedNow != nil {
		recv = *row.ReceivedNow
	}
	// The paid line is the programs' paying in the currency most is paid in.
	lead := moneySum{Currency: "USD"}
	if len(row.Paid) > 0 {
		lead = row.Paid[0]
	}
	var paid []store.ProgramDay
	for _, d := range daily[store.DayPaid90d] {
		if cmp.Or(d.Currency, "USD") == lead.Currency {
			paid = append(paid, d)
		}
	}
	row.Received, row.ReceivedSince, row.receivedDays = linkedChart(daily[store.DayReports90d], float64(recv), "Reports received in 90 days, by day",
		func(v float64) string { return "≈" + formatCount(v, "") + " received in 90 days" })
	row.PaidLine, row.PaidSince, row.paidDays = linkedChart(paid, lead.Amount, "Bounties paid in 90 days, by day, "+lead.Currency+" programs",
		func(v float64) string { return "≈" + humanize.Money(v, lead.Currency) + " paid in 90 days" })
}

// linkedChart charts programs' daily values as a total per day ending at
// end (linkedDays); nil and the first day saved while that's under
// statsDayMin, or without a total. The days charted come back too.
func linkedChart(days []store.ProgramDay, end float64, label string, say func(float64) string) (*lineChart, *time.Time, []store.DayValue) {
	line := linkedDays(days, end)
	if len(line) == 0 {
		return nil, nil, nil
	}
	o := chartOpts{Label: label, MinSpan: statsDayMin, Floor: true}
	if c := buildDayChart(line, o, say); c != nil {
		return c, nil, line
	}
	return nil, &line[0].At, line
}

// linkedDays turns programs' daily values into a total per day. Programs
// come and go (HuntDeck started saving some later; a poll can stop part
// way through a day), so each day is linked to the one before over the
// programs both have, and the line is scaled to end at end, the total now.
// A day with fewer than half the programs of the day before is skipped,
// except the last; one with none of them starts the line again.
func linkedDays(days []store.ProgramDay, end float64) []store.DayValue {
	if end <= 0 || len(days) == 0 {
		return nil
	}
	byDay := map[time.Time]map[int64]float64{}
	for _, d := range days {
		if byDay[d.Day] == nil {
			byDay[d.Day] = map[int64]float64{}
		}
		byDay[d.Day][d.ProgramID] = d.Value
	}
	order := make([]time.Time, 0, len(byDay))
	for t := range byDay {
		order = append(order, t)
	}
	slices.SortFunc(order, func(a, b time.Time) int { return a.Compare(b) })
	var out []store.DayValue
	var prev map[int64]float64
	index := 1.0
	for i, t := range order {
		cur := byDay[t]
		if prev != nil {
			// A thin day is skipped, unless it's the last: today, still
			// being saved, linked over the programs it has so far.
			if 2*len(cur) < len(prev) && i < len(order)-1 {
				continue
			}
			var before, after float64
			common := 0
			for id, v := range cur {
				if p, ok := prev[id]; ok {
					before, after, common = before+p, after+v, common+1
				}
			}
			switch {
			case common == 0: // nothing links it to the days before: the line starts again
				out, index = out[:0], 1
			case before > 0:
				index *= after / before
			}
		}
		out = append(out, store.DayValue{At: t, Value: index})
		prev = cur
	}
	if index <= 0 {
		return nil
	}
	for i := range out {
		out[i].Value = out[i].Value / index * end
	}
	return out
}

// decisionChart charts programs' average hours to a decision as the
// median of them in days, by day; a day with fewer than half the programs
// of the day before is skipped. It returns the line (nil under
// statsDayMin), the median now, the first day while there's no line, and
// the days charted.
func decisionChart(hours []store.ProgramDay) (*lineChart, *float64, *time.Time, []store.DayValue) {
	byDay := map[time.Time][]float64{}
	for _, d := range hours {
		byDay[d.Day] = append(byDay[d.Day], d.Value/24)
	}
	order := slices.SortedFunc(maps.Keys(byDay), func(a, b time.Time) int { return a.Compare(b) })
	var line []store.DayValue
	prev := 0
	for _, t := range order {
		if 2*len(byDay[t]) < prev {
			continue
		}
		prev = len(byDay[t])
		line = append(line, store.DayValue{At: t, Value: *medianOf(byDay[t])})
	}
	if len(line) == 0 {
		return nil, nil, nil, nil
	}
	now := line[len(line)-1].Value
	o := chartOpts{Label: "Median of programs' average days to a decision over 90 days, by day", MinSpan: statsDayMin, Floor: true, LowerBetter: true,
		Format: humanize.Days}
	if c := buildDayChart(line, o, func(v float64) string { return humanize.Days(v) + " to a decision" }); c != nil {
		return c, &now, nil, line
	}
	return nil, &now, &line[0].At, line
}

// medianOf is the median of v, nil when empty.
func medianOf(v []float64) *float64 {
	if len(v) == 0 {
		return nil
	}
	s := slices.Clone(v)
	slices.Sort(s)
	m := s[len(s)/2]
	if len(s)%2 == 0 {
		m = (s[len(s)/2-1] + m) / 2
	}
	return &m
}

// statsFilterFrom reads the program stats filter of /api/v1/stats.
func statsFilterFrom(q url.Values) store.ProgramFilter {
	f := programFilterFrom(q)
	f.Sort = oneOf(q.Get("sort"), store.StatsSorts...)
	return f
}
