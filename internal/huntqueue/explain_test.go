package huntqueue

import (
	"math"
	"testing"
	"time"
)

func at() (time.Time, time.Time) {
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	return now, now.AddDate(0, 0, -10)
}

// The effects multiply back to the score: that is the whole point of printing
// them, so a reader can see which factor cost the most.
func TestExplainEffectsMultiplyToTheScore(t *testing.T) {
	now, added := at()
	in := Inputs{ProgramValue: 72, PayoutUSD: 9000, MedianDays: 12, Disclosed365: 8, InScope: 120, AddedAt: added, Now: now}
	score, factors := Explain(in)
	if score != Score(in).Score {
		t.Fatalf("explain %v vs score %v", score, Score(in).Score)
	}
	if len(factors) != 7 {
		t.Fatalf("%d factors, want 7", len(factors))
	}
	product := 100.0
	for _, f := range factors {
		if f.Effect < 0 || f.Effect > 1 {
			t.Errorf("%s effect %.3f is outside 0..1", f.Name, f.Effect)
		}
		product *= f.Effect
	}
	if math.Abs(product-score) > 0.5 {
		t.Errorf("effects multiply to %.1f, score is %.1f", product, score)
	}
}

// A factor at its floor is reported, not hidden behind a logarithm.
func TestExplainWithAFloorFactor(t *testing.T) {
	now, added := at()
	in := Inputs{ProgramValue: 0, PayoutUSD: 0, MedianDays: 12, InScope: 10, AddedAt: added, Now: now}
	score, factors := Explain(in)
	if score <= 0 {
		t.Fatalf("the score is not floored: %v", score)
	}
	for _, f := range factors {
		if f.Name == "quality" || f.Name == "money" {
			if f.Effect >= 0.9 {
				t.Errorf("%s at its floor barely bit: %.3f", f.Name, f.Effect)
			}
		}
	}
}
