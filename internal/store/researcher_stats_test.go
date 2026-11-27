package store

import (
	"context"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

func TestResearcherStatsRoundTrip(t *testing.T) {
	now := time.Date(2026, 11, 28, 3, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })
	ctx := context.Background()

	got, err := s.ResearcherStats(ctx, "hackerone")
	if err != nil || got != nil {
		t.Fatalf("empty ResearcherStats = (%v, %v), want (nil, nil)", got, err)
	}

	negSig := -2.67
	posSig := 3.25
	pct := 74
	rep := 7
	rank := 42
	reset := false
	in := platform.ResearcherStats{
		Username:           "alice_example",
		TrialResetOnTriage: &reset,
		Last90Days: platform.ResearcherWindowStats{
			Reputation: &rep,
		},
		PastYear: platform.ResearcherWindowStats{
			Signal: &negSig,
		},
		AllTime: platform.ResearcherWindowStats{
			Signal:           &posSig,
			SignalPercentile: &pct,
			Rank:             &rank,
		},
	}
	if err := s.SaveResearcherStats(ctx, "hackerone", in); err != nil {
		t.Fatal(err)
	}

	got, err = s.ResearcherStats(ctx, "hackerone")
	if err != nil || got == nil {
		t.Fatalf("ResearcherStats() = (%v, %v)", got, err)
	}
	if got.Platform != "hackerone" || got.Username != "alice_example" || got.CapturedAt.IsZero() {
		t.Errorf("metadata mismatch: %+v", got)
	}
	if got.PastYear.Signal == nil || *got.PastYear.Signal != -2.67 {
		t.Errorf("past year signal = %v, want -2.67", got.PastYear.Signal)
	}
	if got.Last90Days.Signal != nil || got.Last90Days.Reputation == nil || *got.Last90Days.Reputation != 7 {
		t.Errorf("last 90d = %+v", got.Last90Days)
	}
	if got.AllTime.Rank == nil || *got.AllTime.Rank != 42 || got.AllTime.SignalPercentile == nil || *got.AllTime.SignalPercentile != 74 {
		t.Errorf("all time = %+v", got.AllTime)
	}

	// Upsert overwrites cleanly at a later timestamp.
	now = now.Add(2 * time.Hour)
	updatedSig := 2.40
	in.PastYear.Signal = &updatedSig
	if err := s.SaveResearcherStats(ctx, "hackerone", in); err != nil {
		t.Fatal(err)
	}
	got2, err := s.ResearcherStats(ctx, "hackerone")
	if err != nil || got2 == nil {
		t.Fatal(err)
	}
	if got2.PastYear.Signal == nil || *got2.PastYear.Signal != 2.40 {
		t.Errorf("updated signal = %v, want 2.40", got2.PastYear.Signal)
	}
	if !got2.CapturedAt.Equal(now) {
		t.Errorf("capturedAt = %v, want %v", got2.CapturedAt, now)
	}
}
