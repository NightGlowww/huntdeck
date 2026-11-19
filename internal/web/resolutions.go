package web

import (
	"strconv"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/store"
)

// The resolved-reports panel charts a program's resolutions, its shown
// bounties and its time to resolve per month (lineChart) next to the last
// 90 days compared with the 90 before.

// resolutionMonths is how many months the charts cover.
const resolutionMonths = 12

// submittedOpen is how many recent months of reports counted by when they
// were submitted are still filling in: most reports take a few months to
// be resolved or accepted.
const submittedOpen = 3

// resolutionView is what the program page shows about resolved reports.
type resolutionView struct {
	store.ResolutionSummary
	Chart     lineChart // resolved per month
	Submitted lineChart // the same reports by the month they were submitted
	// Bounties are the bounties shown per month and Median the median days
	// to resolve; nil without any.
	Bounties, Median *lineChart
	// Change in resolved reports and median days, last 90 days vs the 90
	// before: "+12", "−3d 12h"; "" when there's nothing to compare.
	CountDelta, CountDir   string
	MedianDelta, MedianDir string
	// MonthMedian is the last full month's median days to resolve, as the
	// Stats pages give it (nil if none was resolved then), in MonthName,
	// with its backlog note.
	MonthMedian             *float64
	MonthName, MonthNoteKey string
}

func buildResolutionView(sum store.ResolutionSummary, currency string) resolutionView {
	v := resolutionView{ResolutionSummary: sum}
	if sum.Prev90d > 0 || sum.Last90d > 0 {
		v.CountDelta, v.CountDir = signed(float64(sum.Last90d-sum.Prev90d), func(f float64) string { return strconv.Itoa(int(f)) })
	}
	if sum.Median90d != nil && sum.MedianPrev != nil {
		v.MedianDelta, v.MedianDir = signed(*sum.Median90d-*sum.MedianPrev, humanize.Days)
	}

	if n := len(sum.Months); n >= 2 { // the last is this month, still open
		last := sum.Months[n-2]
		v.MonthName, v.MonthMedian = last.Month.Format("January"), last.MedianDays
		if last.MedianDays != nil {
			v.MonthNoteKey = backlogNote(*last.MedianDays)
		}
	}

	months := make([]time.Time, len(sum.Months))
	var resolved, submitted, bounties, median []chartValue
	paid, timed := false, false
	for i, m := range sum.Months {
		months[i] = m.Month
		at := m.Month.Format("Jan 2006")
		resolved = append(resolved, chartValue{float64(m.Resolved), chartTip(at, strconv.Itoa(m.Resolved)+" resolved"), true})
		submitted = append(submitted, chartValue{float64(m.Submitted), chartTip(at, strconv.Itoa(m.Submitted)+" submitted"), true})
		tip := humanize.MoneyFull(m.Bounties, currency) + " shown"
		if sum.BountyAverage != nil {
			tip = "≈" + humanize.Money(m.Bounties, currency) + ": " + strconv.Itoa(m.Rewarded) + " bounties × the average"
		}
		bounties = append(bounties, chartValue{m.Bounties, chartTip(at, tip), true})
		paid = paid || m.Bounties > 0
		if m.MedianDays != nil {
			median = append(median, chartValue{*m.MedianDays, chartTip(at, humanize.Days(*m.MedianDays)+" median to resolve"), true})
			timed = true
		} else {
			median = append(median, chartValue{Tip: chartTip(at, "none resolved"), Known: false})
		}
	}
	v.Chart = buildMonthChart(months, resolved, chartOpts{Label: "Reports resolved per month", Open: 1})
	v.Submitted = buildMonthChart(months, submitted, chartOpts{Label: "Resolved reports by the month they were submitted", Open: submittedOpen})
	if paid {
		label := "Bounties per month, shown on Hacktivity"
		if sum.BountyAverage != nil {
			label = "Bounties per month, estimated at the program's average bounty"
		}
		c := buildMonthChart(months, bounties, chartOpts{Label: label, Open: 1, Format: moneyAxis(currency)})
		v.Bounties = &c
	}
	if timed {
		c := buildMonthChart(months, median, chartOpts{Label: "Median days to resolve, by the month resolved", Open: 1,
			Format: humanize.Days, LowerBetter: true})
		v.Median = &c
	}
	return v
}

// signed formats a change with its sign and says which way it went.
func signed(d float64, format func(float64) string) (string, string) {
	switch {
	case d > 0:
		return "+" + format(d), "up"
	case d < 0:
		return "−" + format(-d), "down"
	}
	return "no change", ""
}

// moneyAxis labels a money chart compactly: "$10.8k".
func moneyAxis(currency string) func(float64) string {
	return func(f float64) string { return humanize.Money(f, currency) }
}
