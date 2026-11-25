// Package reports works out what the user's own reports mean: which are
// open, valid or decided, which need the user, which have gone quiet, what
// they earned, and the Analytics page's numbers. It reads stored reports
// (store.MyReport) and does no SQL or I/O of its own.
package reports

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// DefaultQuietDays is how many days an undecided report can go without a
// word from the program before it's flagged as gone quiet, unless
// Settings › Reports says otherwise (store.ReportQuietDays).
const DefaultQuietDays = 14

// States in the order lists and charts show them, with their labels.
var States = []struct{ Key, Label string }{
	{platform.ReportNew, "New"},
	{platform.ReportTriaged, "Triaged"},
	{platform.ReportAccepted, "Accepted"},
	{platform.ReportResolved, "Resolved"},
	{platform.ReportAcceptedRisk, "Won't fix"},
	{platform.ReportDuplicate, "Duplicate"},
	{platform.ReportInformative, "Informative"},
	{platform.ReportNotApplicable, "Not applicable"},
	{platform.ReportOutOfScope, "Out of scope"},
	{platform.ReportSpam, "Spam"},
}

// Label is a state's name as pages show it.
func Label(state string) string {
	for _, s := range States {
		if s.Key == state {
			return s.Label
		}
	}
	return state
}

// Class is a state's chip class: open ones neutral, valid ones good,
// duplicates and informative ones a warning, the rest bad.
func Class(state string) string {
	switch {
	case Open(state):
		return "open"
	case Valid(state):
		return "valid"
	case state == platform.ReportDuplicate, state == platform.ReportInformative:
		return "dup"
	}
	return "rejected"
}

// Open reports whether nobody has decided the report yet, or it's valid
// and waiting for a fix.
func Open(state string) bool {
	return state == platform.ReportNew || state == platform.ReportTriaged || state == platform.ReportAccepted
}

// Valid reports whether the program accepted the report as a real
// vulnerability: accepted, resolved, or valid but not to be fixed.
func Valid(state string) bool {
	return state == platform.ReportAccepted || state == platform.ReportResolved || state == platform.ReportAcceptedRisk
}

// Decided reports whether the report has an outcome: anything past new and
// triaged (an accepted report is decided valid, though still open).
func Decided(state string) bool {
	return state != platform.ReportNew && state != platform.ReportTriaged && state != ""
}

// Quiet reports whether the user put the report out of mind: muted, or
// snoozed until later than now.
func Quiet(r store.MyReport, now time.Time) bool {
	return r.Muted || (r.SnoozedUntil != nil && r.SnoozedUntil.After(now))
}

// NeedsYou reports whether the program is waiting on the user.
func NeedsYou(r store.MyReport, now time.Time) bool {
	return Open(r.State) && r.WaitingOn == "you" && r.GoneAt == nil && !Quiet(r, now)
}

// Stale reports whether an undecided report (new or triaged) has had no
// word from the program for after, counting from submission when it
// never had one. Accepted reports waiting for a fix are left alone: that
// takes months as a matter of course.
func Stale(r store.MyReport, now time.Time, after time.Duration) bool {
	if Decided(r.State) || r.GoneAt != nil || Quiet(r, now) || r.Source == store.ReportByHand {
		return false
	}
	last := r.SubmittedAt
	if r.LastProgramAt != nil && r.LastProgramAt.After(last) {
		last = *r.LastProgramAt
	}
	return now.Sub(last) >= after
}

// Amount is money in one currency.
type Amount struct {
	Currency string
	Value    float64
}

// Earned totals payments by currency, largest first; keep, if set, picks
// the payments counted.
func Earned(rs []store.MyReport, keep func(store.MyPayment) bool) []Amount {
	by := map[string]float64{}
	for _, r := range rs {
		for _, p := range r.Payments {
			if keep == nil || keep(p) {
				by[p.Currency] += p.Amount
			}
		}
	}
	return amounts(by)
}

func amounts(by map[string]float64) []Amount {
	out := make([]Amount, 0, len(by))
	for c, v := range by {
		if v != 0 {
			out = append(out, Amount{c, v})
		}
	}
	slices.SortFunc(out, func(a, b Amount) int {
		if c := cmp.Compare(b.Value, a.Value); c != 0 {
			return c
		}
		return strings.Compare(a.Currency, b.Currency)
	})
	return out
}

// ReportEarned is one report's payments by currency.
func ReportEarned(r store.MyReport) []Amount {
	return Earned([]store.MyReport{r}, nil)
}

// Severities in order, most severe first, with their labels.
var Severities = []struct{ Key, Label string }{
	{platform.SeverityCritical, "Critical"},
	{platform.SeverityHigh, "High"},
	{platform.SeverityMedium, "Medium"},
	{platform.SeverityLow, "Low"},
	{platform.SeverityNone, "None"},
}

// SeverityRank orders severities: critical 5 down to none 1, unrated 0.
func SeverityRank(s string) int {
	for i, v := range Severities {
		if v.Key == s {
			return len(Severities) - i
		}
	}
	return 0
}

// AgreementOf is how triage treated the severity a report was submitted
// with: "match", "upgraded", "downgraded", "changed" (the platform doesn't
// say from what) or "" (not known).
func AgreementOf(r store.MyReport) string {
	sub, cur := SeverityRank(r.SubmittedSeverity), SeverityRank(r.Severity)
	switch {
	case r.SubmittedSeverity == "" && r.SeverityChanged:
		return "changed"
	case sub == 0 || cur == 0:
		return ""
	case cur > sub:
		return "upgraded"
	case cur < sub:
		return "downgraded"
	}
	return "match"
}
