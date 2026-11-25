package reports

import (
	"math"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

func day(m time.Month, d int) time.Time { return time.Date(2026, m, d, 12, 0, 0, 0, time.UTC) }

func tp(t time.Time) *time.Time { return &t }

func id(n int64) *int64 { return &n }

func sample() []store.MyReport {
	return []store.MyReport{
		{ID: 1, Platform: "hackerone", ProgramID: id(7), ProgramName: "Acme", State: "resolved", Severity: "high", SubmittedSeverity: "medium",
			SubmittedAt: day(1, 10), FirstResponseAt: tp(day(1, 11)), TriagedAt: tp(day(1, 12)), FirstBountyAt: tp(day(1, 20)), ClosedAt: tp(day(2, 9)),
			Payments: []store.MyPayment{{Kind: "bounty", Amount: 1000, Currency: "USD", AwardedAt: day(1, 20)}, {Kind: "bonus", Amount: 100, Currency: "USD", AwardedAt: day(2, 2)}}},
		{ID: 2, Platform: "hackerone", ProgramID: id(7), ProgramName: "Acme", State: "duplicate", Severity: "medium", SubmittedSeverity: "medium",
			SubmittedAt: day(1, 15), FirstResponseAt: tp(day(1, 18))},
		{ID: 3, Platform: "bugcrowd", ProgramName: "Globex", State: "accepted", Severity: "low", SeverityChanged: true,
			SubmittedAt: day(2, 1), TriagedAt: tp(day(2, 3)),
			Payments: []store.MyPayment{{Kind: "bounty", Amount: 300, Currency: "EUR", AwardedAt: day(3, 1)}}},
		{ID: 4, Platform: "intigriti", ProgramName: "Initech", State: "not_applicable", Severity: "", SubmittedSeverity: "high", SubmittedAt: day(3, 5)},
		{ID: 5, Platform: "intigriti", ProgramName: "Initech", State: "new", SubmittedAt: day(3, 20)},
	}
}

func TestAnalyse(t *testing.T) {
	now := day(3, 25)
	pub := map[int64]store.PublishedTime{7: {Triage: func() *float64 { v := 30.0; return &v }()}}
	a := Analyse(sample(), Range{To: now}, time.UTC, pub, now)
	if a.Reports != 5 || a.Decided != 4 || a.Valid != 2 || a.Duplicates != 1 || a.Rejected != 1 || a.Open != 1 {
		t.Errorf("counts: %+v", a)
	}
	if a.Acceptance != .5 || a.DuplicateRate != .25 {
		t.Errorf("rates %v %v", a.Acceptance, a.DuplicateRate)
	}
	if a.Main != "USD" || len(a.Earned) != 2 || a.Earned[0].Value != 1100 || a.Earned[1].Currency != "EUR" {
		t.Errorf("earned %v main %s", a.Earned, a.Main)
	}
	if len(a.Months) != 3 || a.Months[0].Valid != 1 || a.Months[0].Duplicates != 1 || a.Months[0].Earned != 1000 ||
		a.Months[1].Earned != 100 || a.Months[2].Cumulative != 1100 || a.Months[2].Rejected != 1 || a.Months[2].Open != 1 {
		t.Errorf("months %+v", a.Months)
	}
	if a.Agreement != (Agreement{Match: 1, Upgraded: 1, Changed: 1, Unknown: 1}) {
		t.Errorf("agreement %+v", a.Agreement)
	}
	high := a.Severities[1]
	if high.Key != "high" || high.Submitted != 1 || high.Final != 1 || high.Valid != 1 || high.Earned != 1100 || high.Average() != 1100 {
		t.Errorf("high %+v", high)
	}
	if a.Times.Triage.N != 2 || a.Times.Triage.Median != 48 || a.Times.FirstResponse.Mean != 48 || a.Times.Resolution.N != 1 {
		t.Errorf("times %+v", a.Times)
	}
	if len(a.Programs) != 3 || a.Programs[0].Name != "Acme" || a.Programs[0].Reports != 2 || a.Programs[0].Theirs.Triage == nil ||
		a.Programs[0].Yours.Triage.Mean != 48 || a.Programs[1].Name != "Globex" {
		t.Errorf("programs %+v", a.Programs)
	}
	if len(a.Platforms) != 3 || a.Platforms[0].Platform != "hackerone" || a.Platforms[0].Acceptance != .5 {
		t.Errorf("platforms %+v", a.Platforms)
	}

	// March alone: reports submitted then, money awarded then.
	mar := Analyse(sample(), Range{From: day(3, 1).Add(-12 * time.Hour), To: now}, time.UTC, nil, now)
	if mar.Reports != 2 || mar.Valid != 0 || !math.IsNaN(mar.DuplicateRate) == (mar.Decided == 0) || len(mar.Earned) != 1 || mar.Earned[0].Currency != "EUR" {
		t.Errorf("March: %+v", mar)
	}
	if len(mar.Months) != 1 || mar.ThisMonth != 2 {
		t.Errorf("March months %d this month %d", len(mar.Months), mar.ThisMonth)
	}
}

func TestNeedsYouAndStale(t *testing.T) {
	now := day(3, 25)
	for _, tt := range []struct {
		name         string
		r            store.MyReport
		needs, stale bool
		days         int // gone quiet after; DefaultQuietDays when 0
	}{
		{"asked", store.MyReport{State: "new", WaitingOn: "you", SubmittedAt: day(3, 20)}, true, false, 0},
		{"asked but snoozed", store.MyReport{State: "new", WaitingOn: "you", SubmittedAt: day(3, 20), SnoozedUntil: tp(day(4, 1))}, false, false, 0},
		{"quiet triage", store.MyReport{State: "triaged", SubmittedAt: day(2, 1), LastProgramAt: tp(day(3, 1))}, false, true, 0},
		{"recent word", store.MyReport{State: "triaged", SubmittedAt: day(2, 1), LastProgramAt: tp(day(3, 20))}, false, false, 0},
		{"accepted waits for a fix", store.MyReport{State: "accepted", SubmittedAt: day(1, 1)}, false, false, 0},
		{"muted", store.MyReport{State: "new", WaitingOn: "you", Muted: true, SubmittedAt: day(1, 1)}, false, false, 0},
		{"added by hand", store.MyReport{State: "new", Source: store.ReportByHand, SubmittedAt: day(1, 1)}, false, false, 0},
		{"recent word, but quiet after 3 days", store.MyReport{State: "triaged", SubmittedAt: day(2, 1), LastProgramAt: tp(day(3, 20))}, false, true, 3},
		{"quiet triage, but quiet after 30 days", store.MyReport{State: "triaged", SubmittedAt: day(2, 1), LastProgramAt: tp(day(3, 1))}, false, false, 30},
	} {
		days := tt.days
		if days == 0 {
			days = DefaultQuietDays
		}
		if got := NeedsYou(tt.r, now); got != tt.needs {
			t.Errorf("%s: needs you %v", tt.name, got)
		}
		if got := Stale(tt.r, now, time.Duration(days)*24*time.Hour); got != tt.stale {
			t.Errorf("%s: stale %v", tt.name, got)
		}
	}
}
