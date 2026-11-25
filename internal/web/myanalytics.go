package web

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/reports"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Reports › Analytics (/reports/analytics): the user's numbers over a
// range, all time by default. Private, like the Reports page.

// analyticsRanges are the range picker's choices.
var analyticsRanges = []struct{ Key, Label string }{
	{"all", "All time"}, {"month", "This month"}, {"3m", "3 months"}, {"12m", "12 months"}, {"year", "This year"}, {"lastyear", "Last year"},
}

// analyticsRange reads the range from the query: a choice, or from and to
// months (YYYY-MM, both included).
func analyticsRange(q url.Values, now time.Time, zone *time.Location) (reports.Range, string, string) {
	month := func(t time.Time) time.Time {
		t = t.In(zone)
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, zone)
	}
	end := now.Add(time.Second)
	if from, err := time.ParseInLocation("2006-01", q.Get("from"), zone); err == nil {
		to := end
		if t, err := time.ParseInLocation("2006-01", q.Get("to"), zone); err == nil && !t.Before(from) {
			to = t.AddDate(0, 1, 0)
		}
		return reports.Range{From: from, To: to}, "custom", from.Format("Jan 2006") + " – " + to.AddDate(0, 0, -1).Format("Jan 2006")
	}
	this := month(now)
	switch q.Get("range") {
	case "month":
		return reports.Range{From: this, To: end}, "month", this.Format("January 2006")
	case "3m":
		return reports.Range{From: this.AddDate(0, -2, 0), To: end}, "3m", "the last 3 months"
	case "12m":
		return reports.Range{From: this.AddDate(0, -11, 0), To: end}, "12m", "the last 12 months"
	case "year":
		y := time.Date(this.Year(), 1, 1, 0, 0, 0, 0, zone)
		return reports.Range{From: y, To: end}, "year", y.Format("2006")
	case "lastyear":
		y := time.Date(this.Year()-1, 1, 1, 0, 0, 0, 0, zone)
		return reports.Range{From: y, To: y.AddDate(1, 0, 0)}, "lastyear", y.Format("2006")
	}
	return reports.Range{To: end}, "all", "all time"
}

// barChart is a column a month, stacked by outcome (or one series). It's
// drawn in an SVG without a viewBox: across in percent of the width (so
// it fills any box), up and down in pixels (so bars keep square corners
// and the gaps between parts stay 2px however wide the box is). Each
// column has its total over it, and a tooltip.
type barChart struct {
	H      int // the plot's height, px
	Bars   []bar
	Grid   []barGrid
	Ticks  []barTick
	Label  string
	Single bool
}

type bar struct {
	X, W, Mid string // percent of the width: "12.5%"
	SlotX     string // the hover area's left, and SlotW its width
	SlotW     string
	Tip       string
	Total     string // over the bar; "" for none
	TotalY    float64
	Parts     []barPart
	Empty     bool
}

type barPart struct {
	Class string
	Y, H  float64
}

type barGrid struct {
	Y     float64
	Label string
}

type barTick struct {
	At    string // percent of the width
	Label string
}

const (
	barPlotH  = 200.0 // px
	barHead   = 20.0  // room over the tallest bar for its total
	barGutter = 6.0   // percent of the width kept on the right for the axis labels
	barFill   = .58   // a bar's share of its month's width
	partGap   = 2.0   // px between stacked parts
)

// buildBars lays out a value per month; parts are each month's stacked
// values with their classes, from the baseline up; total labels a month's
// column ("" for none).
func buildBars(months []time.Time, parts func(i int) []barPart, totals []float64, tips []string, label string,
	format func(float64) string) barChart {
	c := barChart{H: int(barPlotH), Label: label}
	peak := 0.0
	for _, t := range totals {
		peak = math.Max(peak, t)
	}
	if peak == 0 {
		peak = 1
	}
	nice := niceCeil(peak)
	scale := (barPlotH - barHead) / nice
	c.Grid = append(c.Grid, barGrid{Y: round1(barPlotH - nice*scale), Label: format(nice)})
	if nice >= 2 {
		c.Grid = append(c.Grid, barGrid{Y: round1(barPlotH - nice/2*scale), Label: format(nice / 2)})
	}
	n := float64(len(months))
	slot := (100 - barGutter) / n
	every := max(1, (len(months)+11)/12)
	years := len(months) > 0 && months[0].Year() != months[len(months)-1].Year()
	showTotals := len(months) <= 18
	for i, m := range months {
		left := float64(i) * slot
		w := slot * barFill
		b := bar{X: pct2(left + (slot-w)/2), W: pct2(w), Mid: pct2(left + slot/2), SlotX: pct2(left), SlotW: pct2(slot), Tip: tips[i]}
		y := barPlotH
		for _, p := range parts(i) {
			if p.H <= 0 {
				continue
			}
			h := p.H * scale
			top := y - h
			ph := h
			if y < barPlotH { // a gap under every part but the lowest
				ph -= partGap
			}
			p.Y, p.H = round1(top), round1(math.Max(ph, 1.5))
			b.Parts = append(b.Parts, p)
			y = top
		}
		b.Empty = len(b.Parts) == 0
		if showTotals && totals[i] > 0 {
			b.Total, b.TotalY = format(totals[i]), round1(y-6)
		}
		c.Bars = append(c.Bars, b)
		if i%every == 0 {
			lbl := m.Format("Jan")
			if years && (m.Month() == time.January || i == 0) {
				lbl = m.Format("Jan 06")
			}
			c.Ticks = append(c.Ticks, barTick{At: pct2(left + slot/2), Label: lbl})
		}
	}
	return c
}

// pct2 is a percent with two decimals, for SVG positions.
func pct2(f float64) string { return strconv.FormatFloat(math.Round(f*100)/100, 'f', -1, 64) + "%" }

// niceCeil rounds a peak up to 1, 2 or 5 times a power of ten.
func niceCeil(v float64) float64 {
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 5, 10} {
		if v <= m*p {
			return m * p
		}
	}
	return 10 * p
}

type analyticsView struct {
	reports.Analytics
	RangeKey, RangeLabel string
	From, To             string // the custom range's months
	Ranges               []struct{ Key, Label string }
	Outcomes             barChart
	Money                barChart
	Cumulative           lineChart
	HasMoney             bool
	Total                int // reports stored, whatever the range
	Platforms            []analyticsPlatform
	Programs             []reports.ProgramRow
	Compared             []reports.ProgramRow // programs with both your times and theirs
	Sum                  reports.Month        // the months' outcomes added up, for the legend
}

type analyticsPlatform struct {
	reports.PlatformRow
}

// Pct shows a share, or a dash for none.
func (analyticsView) Pct(f float64) string {
	if math.IsNaN(f) {
		return "—"
	}
	return pct(f)
}

// Hours shows a duration in hours compactly, or "" for none.
func (analyticsView) Hours(s reports.Stat) string {
	if s.N == 0 {
		return ""
	}
	return humanize.Hours(s.Mean)
}

// Published shows a program's published time, or "".
func (analyticsView) Published(h *float64) string {
	if h == nil {
		return ""
	}
	return humanize.Hours(*h)
}

// Versus says whether the user's average is quicker ("good") or slower
// ("bad") than the program's, by more than a fifth.
func (analyticsView) Versus(s reports.Stat, h *float64) string {
	if s.N == 0 || h == nil || *h <= 0 {
		return ""
	}
	switch r := s.Mean / *h; {
	case r < .8:
		return "good"
	case r > 1.25:
		return "bad"
	}
	return "flat"
}

// Share shows n of a total as a percentage.
func (analyticsView) Share(n, of int) string {
	if of == 0 {
		return "—"
	}
	return pct(float64(n) / float64(of))
}

// Median shows a set of times' median, or a dash.
func (analyticsView) Median(s reports.Stat) string {
	if s.N == 0 {
		return "—"
	}
	return humanize.Hours(s.Median)
}

// agreePart is one part of the triage-agreement bar, in a 100-wide box.
type agreePart struct {
	Class, Title string
	X, W         float64
}

// AgreeBar splits a bar by how triage treated the severities.
func (v analyticsView) AgreeBar() []agreePart {
	a := v.Agreement
	k := a.Known()
	if k == 0 {
		return nil
	}
	var out []agreePart
	x := 0.0
	for _, p := range []struct {
		class, title string
		n            int
	}{{"a-match", "Kept as submitted", a.Match}, {"a-up", "Raised by triage", a.Upgraded}, {"a-down", "Lowered by triage", a.Downgraded},
		{"a-changed", "Changed, from what not known", a.Changed}} {
		if p.n == 0 {
			continue
		}
		w := 100 * float64(p.n) / float64(k)
		out = append(out, agreePart{Class: p.class, Title: fmt.Sprintf("%s: %d", p.title, p.n), X: round1(x), W: round1(math.Max(w-.4, .3))})
		x += w
	}
	return out
}

// RangeURL is the page over another range.
func (analyticsView) RangeURL(key string) string {
	if key == "all" {
		return "/reports/analytics"
	}
	return "/reports/analytics?range=" + key
}

func (s *server) reportsAnalytics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	all, err := s.Store.MyReports(ctx, store.ReportFilter{})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var ids []int64
	for _, rep := range all {
		if rep.ProgramID != nil {
			ids = append(ids, *rep.ProgramID)
		}
	}
	published, err := s.Store.PublishedTimes(ctx, ids)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	zone := s.shownZone()
	now := s.Store.Now()
	rg, key, label := analyticsRange(r.URL.Query(), now, zone)
	a := reports.Analyse(all, rg, zone, published, now)
	v := analyticsView{Analytics: a, RangeKey: key, RangeLabel: label, Ranges: analyticsRanges, Total: len(all),
		From: r.URL.Query().Get("from"), To: r.URL.Query().Get("to")}
	months := make([]time.Time, len(a.Months))
	totals := make([]float64, len(a.Months))
	money := make([]float64, len(a.Months))
	tips := make([]string, len(a.Months))
	moneyTips := make([]string, len(a.Months))
	cum := make([]chartValue, len(a.Months))
	for i, m := range a.Months {
		months[i], totals[i], money[i] = m.Start, float64(m.Total()), m.Earned
		v.Sum.Valid, v.Sum.Duplicates, v.Sum.Rejected, v.Sum.Open = v.Sum.Valid+m.Valid, v.Sum.Duplicates+m.Duplicates,
			v.Sum.Rejected+m.Rejected, v.Sum.Open+m.Open
		tip := fmt.Sprintf("%s · %d %s", m.Start.Format("Jan 2006"), m.Total(), plural(m.Total(), "report", "reports"))
		if m.Total() > 0 {
			tip += fmt.Sprintf(": %d valid, %d duplicate, %d rejected, %d open", m.Valid, m.Duplicates, m.Rejected, m.Open)
			var sev []string
			for _, sv := range reports.Severities {
				if n := m.BySeverity[sv.Key]; n > 0 {
					sev = append(sev, fmt.Sprintf("%d %s", n, strings.ToLower(sv.Label)))
				}
			}
			if len(sev) > 0 {
				tip += " (" + strings.Join(sev, ", ") + ")"
			}
		}
		tips[i] = tip
		moneyTips[i] = m.Start.Format("Jan 2006") + " · " + humanize.MoneyFull(m.Earned, a.Main)
		cum[i] = chartValue{V: m.Cumulative, Tip: m.Start.Format("Jan 2006") + " · " + humanize.MoneyFull(m.Cumulative, a.Main) + " in all", Known: true}
		if m.Earned > 0 {
			v.HasMoney = true
		}
	}
	if len(months) > 0 {
		v.Outcomes = buildBars(months, func(i int) []barPart {
			m := a.Months[i]
			return []barPart{{Class: "valid", H: float64(m.Valid)}, {Class: "dup", H: float64(m.Duplicates)},
				{Class: "rejected", H: float64(m.Rejected)}, {Class: "open", H: float64(m.Open)}}
		}, totals, tips, "Reports submitted per month, by outcome", func(f float64) string { return formatCount(f, "") })
		v.Money = buildBars(months, func(i int) []barPart { return []barPart{{Class: "money", H: money[i]}} }, money, moneyTips,
			"Earned per month in "+a.Main, func(f float64) string { return humanize.Money(f, a.Main) })
		v.Money.Single = true
		open := 0
		if rg.To.After(now) {
			open = 1
		}
		v.Cumulative = buildMonthChart(months, cum, chartOpts{Label: "Earned in all, in " + a.Main, Open: open,
			Format: func(f float64) string { return humanize.Money(f, a.Main) }})
	}
	for _, p := range a.Platforms {
		v.Platforms = append(v.Platforms, analyticsPlatform{p})
	}
	for i, p := range a.Programs {
		if i < 15 && (len(p.Earned) > 0 || p.Valid > 0) {
			v.Programs = append(v.Programs, p)
		}
		y, t := p.Yours, p.Theirs
		if (y.FirstResponse.N > 0 && t.FirstResponse != nil) || (y.Triage.N > 0 && (t.Triage != nil || t.Validation != nil)) ||
			(y.Bounty.N > 0 && t.Bounty != nil) || (y.Resolution.N > 0 && t.Resolution != nil) {
			v.Compared = append(v.Compared, p)
		}
	}
	if len(v.Compared) > 25 {
		v.Compared = v.Compared[:25]
	}
	s.render(w, r, http.StatusOK, "reportsanalytics", pageData{Title: "Analytics", Nav: "analytics", Data: v})
}
