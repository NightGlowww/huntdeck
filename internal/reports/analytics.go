package reports

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// The Analytics page's numbers. Reports count by when they were submitted,
// money by when it was awarded; a range keeps both to its months. Money is
// never converted: each currency is its own total, and charts follow the
// currency most was earned in.

// Range is the stretch of time analysed: [From, To). A zero From is all
// time.
type Range struct {
	From, To time.Time
}

func (r Range) has(t time.Time) bool {
	return (r.From.IsZero() || !t.Before(r.From)) && t.Before(r.To)
}

// Analytics is what the Analytics page shows.
type Analytics struct {
	Reports, Decided, Valid, Duplicates, Informative, Rejected, Open int
	// Acceptance is valid of decided, DuplicateRate duplicates of decided:
	// NaN with nothing decided.
	Acceptance, DuplicateRate float64
	Earned                    []Amount
	Main                      string  // the currency most was earned in
	PerWeek, PerMonth         float64 // reports, over the range's length
	EarnedPerMonth            float64 // in Main
	ThisMonth                 int
	ThisMonthEarned           []Amount
	Months                    []Month
	Severities                []SeverityRow
	Agreement                 Agreement
	Platforms                 []PlatformRow
	Programs                  []ProgramRow
	Times                     Times
	Span                      time.Duration // how long the range covers, for the rates
}

// Month is one month's reports by outcome and money awarded in it.
type Month struct {
	Start                             time.Time
	Valid, Duplicates, Rejected, Open int
	BySeverity                        map[string]int
	Earned                            float64 // in Analytics.Main
	Cumulative                        float64 // in Analytics.Main, from the range's start
}

// Total is the month's reports.
func (m Month) Total() int { return m.Valid + m.Duplicates + m.Rejected + m.Open }

// SeverityRow is one severity: reports submitted as it, rated it now,
// valid ones rated it, and what those earned.
type SeverityRow struct {
	Key, Label              string
	Submitted, Final, Valid int
	Earned                  float64 // in Analytics.Main
	Paid                    int     // reports with a payment in Main
}

// Average is the average paid per paid report, in Main.
func (s SeverityRow) Average() float64 {
	if s.Paid == 0 {
		return 0
	}
	return s.Earned / float64(s.Paid)
}

// Agreement counts how triage treated the severity decided reports were
// submitted with.
type Agreement struct {
	Match, Upgraded, Downgraded, Changed, Unknown int
}

// Known is how many reports the comparison covers.
func (a Agreement) Known() int { return a.Match + a.Upgraded + a.Downgraded + a.Changed }

// PlatformRow is one platform's reports.
type PlatformRow struct {
	Platform                            string
	Reports, Decided, Valid, Duplicates int
	Acceptance, DuplicateRate           float64
	Earned                              []Amount
	Triage, Bounty                      Stat
}

// ProgramRow is one program's reports, its earnings and the user's times
// against the ones it publishes.
type ProgramRow struct {
	ProgramID        *int64
	Platform, Handle string
	Name             string
	Private          bool
	Reports, Valid   int
	Duplicates       int
	Earned           []Amount
	MainEarned       float64
	Yours            Times
	Theirs           store.PublishedTime
}

// Stat is a set of durations: their average and median, in hours.
type Stat struct {
	N            int
	Mean, Median float64
}

// Times are the user's times from submission.
type Times struct {
	FirstResponse, Triage, Bounty, Resolution Stat
}

func stat(hours []float64) Stat {
	if len(hours) == 0 {
		return Stat{}
	}
	slices.Sort(hours)
	sum := 0.0
	for _, h := range hours {
		sum += h
	}
	mid := len(hours) / 2
	med := hours[mid]
	if len(hours)%2 == 0 {
		med = (hours[mid-1] + hours[mid]) / 2
	}
	return Stat{N: len(hours), Mean: sum / float64(len(hours)), Median: med}
}

type timeLists struct{ response, triage, bounty, resolution []float64 }

func (l *timeLists) add(r store.MyReport) {
	since := func(t *time.Time) (float64, bool) {
		if t == nil || t.Before(r.SubmittedAt) {
			return 0, false
		}
		return t.Sub(r.SubmittedAt).Hours(), true
	}
	if h, ok := since(r.FirstResponseAt); ok {
		l.response = append(l.response, h)
	}
	if h, ok := since(r.TriagedAt); ok {
		l.triage = append(l.triage, h)
	}
	if h, ok := since(r.FirstBountyAt); ok {
		l.bounty = append(l.bounty, h)
	}
	if r.State == "resolved" {
		if h, ok := since(r.ClosedAt); ok {
			l.resolution = append(l.resolution, h)
		}
	}
}

func (l timeLists) times() Times {
	return Times{FirstResponse: stat(l.response), Triage: stat(l.triage), Bounty: stat(l.bounty), Resolution: stat(l.resolution)}
}

func rate(n, of int) float64 {
	if of == 0 {
		return math.NaN()
	}
	return float64(n) / float64(of)
}

// monthOf is the start of t's month in zone.
func monthOf(t time.Time, zone *time.Location) time.Time {
	t = t.In(zone)
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, zone)
}

// Analyse works out the numbers for the reports in rg, in zone's months;
// published are the programs' published times (store.PublishedTimes),
// now the current time. Reports gone from their platform still count.
func Analyse(all []store.MyReport, rg Range, zone *time.Location, published map[int64]store.PublishedTime, now time.Time) Analytics {
	var a Analytics
	inRange := make([]store.MyReport, 0, len(all))
	for _, r := range all {
		if rg.has(r.SubmittedAt) {
			inRange = append(inRange, r)
		}
	}
	a.Earned = Earned(all, func(p store.MyPayment) bool { return rg.has(p.AwardedAt) })
	if len(a.Earned) > 0 {
		a.Main = a.Earned[0].Currency
	} else if ever := Earned(all, nil); len(ever) > 0 {
		a.Main = ever[0].Currency
	}
	thisMonth := monthOf(now, zone)
	a.ThisMonthEarned = Earned(all, func(p store.MyPayment) bool { return !p.AwardedAt.Before(thisMonth) })

	// The months: from the range's start (or the first report or
	// payment) to now or the range's end.
	first := rg.From
	if first.IsZero() {
		first = now
		for _, r := range all {
			if r.SubmittedAt.Before(first) {
				first = r.SubmittedAt
			}
			for _, p := range r.Payments {
				if p.AwardedAt.Before(first) {
					first = p.AwardedAt
				}
			}
		}
	}
	last := rg.To
	if last.After(now) {
		last = now
	}
	a.Span = last.Sub(first)
	idx := map[time.Time]int{}
	for m := monthOf(first, zone); !m.After(monthOf(last.Add(-time.Nanosecond), zone)); m = m.AddDate(0, 1, 0) {
		idx[m] = len(a.Months)
		a.Months = append(a.Months, Month{Start: m, BySeverity: map[string]int{}})
	}

	sevs := map[string]*SeverityRow{}
	for _, s := range Severities {
		row := SeverityRow{Key: s.Key, Label: s.Label}
		sevs[s.Key] = &row
	}
	plats := map[string]*PlatformRow{}
	platTimes := map[string]*timeLists{}
	progs := map[string]*ProgramRow{}
	progTimes := map[string]*timeLists{}
	var all3 timeLists
	for _, r := range inRange {
		a.Reports++
		valid, open := Valid(r.State), Open(r.State)
		switch {
		case valid:
			a.Valid++
		case r.State == "duplicate":
			a.Duplicates++
		case r.State == "informative":
			a.Informative++
		case open:
		default:
			a.Rejected++
		}
		if open && !valid {
			a.Open++
		}
		if Decided(r.State) {
			a.Decided++
			switch AgreementOf(r) {
			case "match":
				a.Agreement.Match++
			case "upgraded":
				a.Agreement.Upgraded++
			case "downgraded":
				a.Agreement.Downgraded++
			case "changed":
				a.Agreement.Changed++
			default:
				a.Agreement.Unknown++
			}
		}
		if !r.SubmittedAt.Before(thisMonth) {
			a.ThisMonth++
		}
		if i, ok := idx[monthOf(r.SubmittedAt, zone)]; ok {
			m := &a.Months[i]
			switch {
			case valid:
				m.Valid++
			case r.State == "duplicate":
				m.Duplicates++
			case open:
				m.Open++
			default:
				m.Rejected++
			}
			if r.Severity != "" {
				m.BySeverity[r.Severity]++
			}
		}
		mainPaid := 0.0
		for _, p := range r.Payments {
			if p.Currency == a.Main {
				mainPaid += p.Amount
			}
		}
		if s := sevs[r.SubmittedSeverity]; s != nil {
			s.Submitted++
		}
		if s := sevs[r.Severity]; s != nil {
			s.Final++
			if valid {
				s.Valid++
			}
			if mainPaid > 0 {
				s.Earned += mainPaid
				s.Paid++
			}
		}
		all3.add(r)

		pr := plats[r.Platform]
		if pr == nil {
			pr = &PlatformRow{Platform: r.Platform}
			plats[r.Platform], platTimes[r.Platform] = pr, &timeLists{}
		}
		pr.Reports++
		if Decided(r.State) {
			pr.Decided++
		}
		if valid {
			pr.Valid++
		}
		if r.State == "duplicate" {
			pr.Duplicates++
		}
		platTimes[r.Platform].add(r)

		key := r.Platform + "/" + strings.ToLower(r.ProgramHandle+"|"+r.ProgramName)
		if r.ProgramID != nil {
			key = "id/" + strconv.FormatInt(*r.ProgramID, 10)
		}
		g := progs[key]
		if g == nil {
			g = &ProgramRow{ProgramID: r.ProgramID, Platform: r.Platform, Handle: r.ProgramHandle, Name: r.ProgramName, Private: r.ProgramPrivate}
			if g.Name == "" {
				g.Name = r.ProgramHandle
			}
			if r.ProgramID != nil {
				g.Theirs = published[*r.ProgramID]
			}
			progs[key], progTimes[key] = g, &timeLists{}
		}
		g.Reports++
		if valid {
			g.Valid++
		}
		if r.State == "duplicate" {
			g.Duplicates++
		}
		progTimes[key].add(r)
	}
	// Money, by when it was awarded (also for reports submitted earlier).
	for _, r := range all {
		key := r.Platform + "/" + strings.ToLower(r.ProgramHandle+"|"+r.ProgramName)
		if r.ProgramID != nil {
			key = "id/" + strconv.FormatInt(*r.ProgramID, 10)
		}
		var mine []store.MyPayment
		for _, p := range r.Payments {
			if !rg.has(p.AwardedAt) {
				continue
			}
			mine = append(mine, p)
			if p.Currency == a.Main {
				if i, ok := idx[monthOf(p.AwardedAt, zone)]; ok {
					a.Months[i].Earned += p.Amount
				}
			}
		}
		if len(mine) == 0 {
			continue
		}
		one := store.MyReport{Payments: mine}
		if pr := plats[r.Platform]; pr != nil {
			pr.Earned = merge(pr.Earned, Earned([]store.MyReport{one}, nil))
		} else {
			plats[r.Platform] = &PlatformRow{Platform: r.Platform, Earned: Earned([]store.MyReport{one}, nil)}
			platTimes[r.Platform] = &timeLists{}
		}
		g := progs[key]
		if g == nil {
			g = &ProgramRow{ProgramID: r.ProgramID, Platform: r.Platform, Handle: r.ProgramHandle, Name: r.ProgramName, Private: r.ProgramPrivate}
			if g.Name == "" {
				g.Name = r.ProgramHandle
			}
			if r.ProgramID != nil {
				g.Theirs = published[*r.ProgramID]
			}
			progs[key], progTimes[key] = g, &timeLists{}
		}
		g.Earned = merge(g.Earned, Earned([]store.MyReport{one}, nil))
	}
	cum := 0.0
	for i := range a.Months {
		cum += a.Months[i].Earned
		a.Months[i].Cumulative = cum
	}

	a.Acceptance, a.DuplicateRate = rate(a.Valid, a.Decided), rate(a.Duplicates, a.Decided)
	if weeks := a.Span.Hours() / (24 * 7); weeks >= 1 {
		a.PerWeek = float64(a.Reports) / weeks
		a.PerMonth = a.PerWeek * 52 / 12
		mainTotal := 0.0
		for _, e := range a.Earned {
			if e.Currency == a.Main {
				mainTotal = e.Value
			}
		}
		a.EarnedPerMonth = mainTotal / (weeks * 12 / 52)
	}
	for _, s := range Severities {
		a.Severities = append(a.Severities, *sevs[s.Key])
	}
	a.Times = all3.times()
	for p, row := range plats {
		row.Acceptance, row.DuplicateRate = rate(row.Valid, row.Decided), rate(row.Duplicates, row.Decided)
		t := platTimes[p].times()
		row.Triage, row.Bounty = t.Triage, t.Bounty
		a.Platforms = append(a.Platforms, *row)
	}
	slices.SortFunc(a.Platforms, func(x, y PlatformRow) int {
		return cmp.Or(cmp.Compare(y.Reports, x.Reports), strings.Compare(x.Platform, y.Platform))
	})
	for key, g := range progs {
		g.Yours = progTimes[key].times()
		for _, e := range g.Earned {
			if e.Currency == a.Main {
				g.MainEarned = e.Value
			}
		}
		a.Programs = append(a.Programs, *g)
	}
	slices.SortFunc(a.Programs, func(x, y ProgramRow) int {
		return cmp.Or(cmp.Compare(y.MainEarned, x.MainEarned), cmp.Compare(len(y.Earned), len(x.Earned)),
			cmp.Compare(y.Valid, x.Valid), cmp.Compare(y.Reports, x.Reports), strings.Compare(x.Name, y.Name))
	})
	return a
}

// merge adds b's amounts to a's.
func merge(a, b []Amount) []Amount {
	by := map[string]float64{}
	for _, x := range a {
		by[x.Currency] += x.Value
	}
	for _, x := range b {
		by[x.Currency] += x.Value
	}
	return amounts(by)
}
