package web

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/store"
)

// programRow is one program on the programs page with its trend lines.
// On wide screens a line stands in for its number (the number is in the
// line's tooltip); without a line the number shows.
type programRow struct {
	store.ProgramListItem
	Trends programTrends
	// Discloses says its platform publishes disclosed reports, so none is
	// a real 0.
	Discloses bool
}

// programTrends are a program's numbers over time, where its platform
// shares them.
type programTrends struct {
	Received spark // reports received in 90 days (HackerOne's daily stats)
	Paid     spark // bounties paid in 90 days (HackerOne's daily stats)
	Resolved spark // reports resolved in 90 days (Hacktivity, CrowdStream)
}

// spark is a chart of a number over the last 90 days, stock-site style.
// Dir is which way it went ("up", "down" or "flat"); Tone colours it
// that way, up green and down red whatever the number ("good", "bad" or
// "flat"); the tooltip gives the change in words. Line and Area are SVG paths; Line is
// "" with fewer than two points or none above zero.
type spark struct {
	Line, Area string
	Gap        string // dotted links across dry spells (dryRuns)
	Last       sparkPoint
	Dir, Tone  string
	Change     string // "+12%", "−8%", "±0%"; "" without a line
	Since      string // what Change compares with: "90 days ago" or the first day's date
	Summary    string // the whole chart in words, for its tooltip
	Heading    string // what the line is
	// Days are its points for the instant tooltip (app.js): "x|y|tip"
	// joined by ";", in the chart's viewBox units.
	Days string
	// points are the days charted, for the API; nil without a line.
	points []store.DayValue
	// Provisional marks an early line for a figure still being collected
	// (collectingSpark), drawn dashed; Collecting, without a line, is the
	// dotted placeholder's tooltip: for a figure still being collected, or
	// (FirstDay) one with a single day saved, shown beside its number.
	Provisional bool
	Collecting  string
	FirstDay    bool
}

// trendMetric says how a trend line words its values.
type trendMetric struct {
	value   func(float64) string // a value on its own: "49", "$35,750"
	words   string               // what it is: "resolved in 90 days"
	heading string               // what the line is: "Valid reports · 90 days"
}

// The trend charts' viewBox.
const trendW, trendH = 100.0, 30.0

// Every trend chart covers the last 90 days (window90), so the columns
// compare like with like, with a point a day (trendStep): the snapshots
// are daily, and charts counted from report dates count every day.
const (
	window90  = 90 * 24 * time.Hour
	trendStep = 24 * time.Hour
)

// programRows adds trends to a page of programs.
func (s *server) programRows(ctx context.Context, items []store.ProgramListItem, now time.Time) ([]programRow, error) {
	ids := make([]int64, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	from := now.Add(-window90)
	times, err := s.Store.ReportTimes(ctx, ids, from.Add(-window90))
	if err != nil {
		return nil, err
	}
	// One scan for every daily column: the page draws a line per program, and
	// reading each column separately meant seven passes over the same rows.
	snap, err := s.Store.SnapshotsFor(ctx, ids, from)
	if err != nil {
		return nil, err
	}
	received, paid, avgs := snap.Reports90d, snap.Paid90d, snap.AvgPayout
	totals := store.TotalsHistory{Reports: snap.Reports, Paid: snap.Paid, Rewarded: snap.Rewarded, Accepted: snap.Accepted}
	out := make([]programRow, len(items))
	for i, it := range items {
		out[i] = programRow{ProgramListItem: it, Discloses: s.discloses(it.Platform)}
		var counted reportSeries
		if t, ok := times[it.ID]; ok {
			counted = reportCounts(t, from, now, trendStep)
		}
		count := func(n float64) string { return formatCount(n, "") }
		receivedValue := count
		if it.Reports90dIsEstimate() {
			receivedValue = func(n float64) string { return "≈" + count(n) }
		}
		metrics := struct{ received, paid, paidEst, resolved, validEst trendMetric }{
			received: trendMetric{value: receivedValue, words: "reports received in 90 days", heading: "Reports received · 90 days"},
			paid: trendMetric{value: func(n float64) string { return humanize.MoneyFull(n, it.StatsCurrency) }, words: "paid in 90 days",
				heading: "Bounties paid · 90 days"},
			paidEst: trendMetric{value: func(n float64) string { return "≈" + humanize.Money(n, it.StatsCurrency) }, words: "paid in 90 days (estimate)",
				heading: "Bounties paid · 90 days, estimated"},
			resolved: trendMetric{value: count, words: "resolved in 90 days", heading: "Valid reports · 90 days"},
			validEst: trendMetric{value: func(n float64) string { return "≈" + count(n) }, words: "valid in 90 days (estimate)",
				heading: "Valid reports · 90 days, estimated"},
		}
		t := &out[i].Trends
		b := func(days []store.DayValue, m trendMetric) spark {
			if sp := buildSpark(days, trendW, trendH, m); sp.Line != "" {
				return sp
			}
			return firstDaySpark(days, m)
		}
		// Figures worked out from saved totals start their lines with the
		// first totals, not a week later (backfillCounted).
		receivedDays := received[it.ID]
		if it.CountedDays != nil {
			receivedDays = withBackfill(totals.Reports[it.ID], receivedDays, it.StartedAt)
		}
		if it.Reports90d != nil {
			t.Received = b(receivedDays, metrics.received)
		} else if it.TotalReports != nil { // the cell says Pending: collecting
			early := metrics.received
			early.value = func(n float64) string { return "≈" + count(n) }
			t.Received = collectingSpark(totals.Reports[it.ID], it.StartedAt, early, "reports received", it.Platform)
		}
		switch {
		case it.Paid90dEstimated && it.AvgPayout != nil:
			t.Paid = b(paidEstimate(counted.acceptedPaid, avgs[it.ID], *it.AvgPayout), metrics.paidEst)
		case it.Paid90d != nil && it.Paid90dIsEstimate() && it.Platform == "bugcrowd" && it.AvgPayout != nil:
			// From rewarded reports × the average payout of each day.
			early := paidEstimate(backfillCounted(totals.Rewarded[it.ID], paid[it.ID], it.StartedAt), avgs[it.ID], *it.AvgPayout)
			t.Paid = b(append(early, paid[it.ID]...), metrics.paidEst)
		case it.Paid90d != nil && it.Paid90dIsEstimate():
			t.Paid = b(withBackfill(totals.Paid[it.ID], paid[it.ID], it.StartedAt), metrics.paidEst)
		case it.Paid90d != nil:
			t.Paid = b(paid[it.ID], metrics.paid)
		case collectingPaid(it):
			t.Paid = collectingSpark(totals.Paid[it.ID], it.StartedAt, metrics.paidEst, "bounties paid", it.Platform)
		}
		switch {
		case it.Resolved90dEstimated:
			// Received × the share accepted, as the estimate is, each day.
			t.Resolved = b(validDays(receivedDays, totals.Accepted[it.ID], totals.Reports[it.ID]), metrics.validEst)
		case it.Resolved90d != nil:
			t.Resolved = b(counted.resolved, metrics.resolved)
		}
	}
	return out, nil
}

// reportSeries are a program's numbers counted from report dates: resolved,
// and accepted (all, and the payable ones) where the platform lists those,
// in the 90 days up to each point.
type reportSeries struct {
	resolved, accepted, acceptedPaid []store.DayValue
}

// reportCounts counts t's reports in the 90 days up to each point from from
// to now, a point every step. A feed that starts partway (CrowdStream) is
// counted only from 90 days into it, so each point is a whole 90 days;
// accepted is nil without such a feed.
func reportCounts(t store.ReportTimes, from, now time.Time, step time.Duration) reportSeries {
	if t.CompleteFrom != nil && t.CompleteFrom.Add(window90).After(from) {
		from = t.CompleteFrom.Add(window90)
	}
	out := reportSeries{resolved: rolling90(t.Resolved, from, now, step)}
	if t.CompleteFrom != nil {
		out.accepted = rolling90(t.Accepted, from, now, step)
		out.acceptedPaid = rolling90(t.AcceptedPaid, from, now, step)
	}
	return out
}

// paidEstimate estimates bounties paid in the 90 days up to each point of
// accepted (payable accepted reports in those 90 days) as that count times
// the average payout then: the latest saved on or before the day, else the
// earliest saved, else avg.
func paidEstimate(accepted, avgs []store.DayValue, avg float64) []store.DayValue {
	out := make([]store.DayValue, len(accepted))
	j := 0
	for i, d := range accepted {
		for j < len(avgs) && !avgs[j].At.After(d.At) {
			j++
		}
		a := avg
		switch {
		case j > 0:
			a = avgs[j-1].Value
		case len(avgs) > 0:
			a = avgs[0].Value
		}
		out[i] = store.DayValue{At: d.At, Value: d.Value * a}
	}
	return out
}

// rolling90 counts, every step back from now to from, the times in the 90
// days up to that point; times are oldest first. Oldest point first.
func rolling90(times []time.Time, from, now time.Time, step time.Duration) []store.DayValue {
	if !from.Before(now) {
		return nil
	}
	var at []time.Time
	for t := now; !t.Before(from); t = t.Add(-step) {
		at = append(at, t)
	}
	slices.Reverse(at)
	out := make([]store.DayValue, len(at))
	lo, hi := 0, 0 // times[lo:hi] are in the window ending at the current point
	for i, t := range at {
		for hi < len(times) && !times[hi].After(t) {
			hi++
		}
		for lo < hi && !times[lo].After(t.Add(-window90)) {
			lo++
		}
		out[i] = store.DayValue{At: t, Value: float64(hi - lo)}
	}
	return out
}

// buildSpark charts a number over time at the given size.
func buildSpark(days []store.DayValue, w, h float64, m trendMetric) spark {
	if len(days) == 0 {
		return spark{} // nothing to show, not even a placeholder
	}
	sp := spark{Heading: m.heading}
	days = thin(days, 91) // every day of the 90
	if len(days) < 2 {
		return spark{}
	}
	if !slices.ContainsFunc(days, func(d store.DayValue) bool { return d.Value != 0 }) {
		return spark{} // none in the last 90 days
	}
	line := buildLine(days, w, h, func(d store.DayValue) string { return d.At.UTC().Format("Jan 2, 2006") + ": " + m.say(d.Value) })
	sp.Line, sp.Last, sp.Area, sp.Gap = line.Line, line.Last, line.Area, line.Gap
	sp.Days = sparkDays(days, line.Points, m)
	sp.points = days

	// The change over the last 90 days, or since the first point if the line
	// is shorter than that.
	last, base := days[len(days)-1], days[0]
	sp.Since = base.At.UTC().Format("Jan 2")
	for _, d := range days {
		if d.At.After(last.At.Add(-window90).Add(12 * time.Hour)) {
			break
		}
		base, sp.Since = d, "90 days ago"
	}
	sp.Dir = "flat"
	switch diff := last.Value - base.Value; {
	case base.Value == 0 && last.Value == 0:
		sp.Change = "±0%"
	case base.Value == 0:
		sp.Dir, sp.Change = "up", "from 0"
	default:
		pct := diff / base.Value * 100
		switch {
		case pct >= 5:
			sp.Dir = "up"
		case pct <= -5:
			sp.Dir = "down"
		}
		sp.Change = fmt.Sprintf("%+.0f%%", pct)
		if math.Round(pct) == 0 {
			sp.Change = "±0%"
		}
	}
	sp.Change = strings.Replace(sp.Change, "-", "−", 1)
	sp.Tone = "flat"
	if sp.Dir != "flat" {
		sp.Tone = map[string]string{"up": "good", "down": "bad"}[sp.Dir] // up green, down red, whatever the number
	}
	sp.Summary = fmt.Sprintf("%s now, %s on %s (%s then)", m.say(last.Value), sp.Change, sp.Since, m.value(base.Value))
	return sp
}

// sparkDays gives each point for the instant tooltip (app.js): where it
// is and its date and value, "x|y|Aug 13 · 49". Days in a row with the
// same value are one stretch, "x0~x1|y|Aug 13 – Sep 1 · 49": a number
// that stays put for weeks doesn't repeat itself for every day of them.
func sparkDays(days []store.DayValue, pts []sparkPoint, m trendMetric) string {
	var b strings.Builder
	for i := 0; i < len(days); {
		v := m.value(days[i].Value)
		j := i
		for j+1 < len(days) && pts[j+1].Y == pts[i].Y && m.value(days[j+1].Value) == v {
			j++
		}
		if i > 0 {
			b.WriteByte(';')
		}
		when := days[i].At.UTC().Format("Jan 2")
		if j > i {
			fmt.Fprintf(&b, "%g~", pts[i].X)
			when += " – " + days[j].At.UTC().Format("Jan 2")
		}
		fmt.Fprintf(&b, "%g|%g|%s", pts[j].X, pts[i].Y, chartTip(when, v))
		i = j + 1
	}
	return b.String()
}

// say is a value in words: "49 resolved in 90 days".
func (m trendMetric) say(v float64) string {
	return m.value(v) + " " + m.words
}

// thin keeps at most n of days, evenly spread, always with the last.
func thin(days []store.DayValue, n int) []store.DayValue {
	if len(days) <= n {
		return days
	}
	out := make([]store.DayValue, 0, n)
	for i := range n {
		out = append(out, days[i*(len(days)-1)/(n-1)])
	}
	return out
}

// sparkPoint is one day on a sparkline, with the strip of chart that shows
// its tooltip.
type sparkPoint struct {
	X, Y       float64
	HitX, HitW float64
	Title      string
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// formatCount writes a count with thousands separators: "1,204".
func formatCount(v float64, _ string) string {
	return thousands(strconv.FormatFloat(math.Round(v), 'f', 0, 64))
}
