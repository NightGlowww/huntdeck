package huntqueue

import (
	"math"
	"testing"
	"time"
)

func base() Inputs {
	return Inputs{
		ProgramValue: 70,
		PayoutUSD:    5000,
		MedianDays:   12,
		Disclosed365: 3,
		InScope:      120,
		AddedAt:      time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		Now:          time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC),
	}
}

func TestMoneyRaisesTheScore(t *testing.T) {
	lo, hi := base(), base()
	lo.PayoutUSD, hi.PayoutUSD = 500, 20000
	if Score(lo).Score >= Score(hi).Score {
		t.Fatalf("$500 (%.1f) did not rank below $20k (%.1f)", Score(lo).Score, Score(hi).Score)
	}
}

func TestFreshnessDecays(t *testing.T) {
	fresh, stale := base(), base()
	stale.AddedAt = stale.Now.AddDate(0, 0, -63)
	if Score(fresh).Score <= Score(stale).Score {
		t.Fatalf("fresh %.1f did not beat stale %.1f", Score(fresh).Score, Score(stale).Score)
	}
}

// The geometric mean cannot be bought out: a crowded program with a huge
// bounty must not reach the top of the queue on money alone.
func TestCrowdedProgramIsNotBoughtOut(t *testing.T) {
	in := base()
	in.InScope = 10
	in.Disclosed365 = 120
	in.PayoutUSD = 50000
	if got := Score(in).Score; got > 60 {
		t.Errorf("a program with 12 disclosures per asset scored %.1f", got)
	}
}

func TestPersonalFactorIsConstantWithoutHistory(t *testing.T) {
	a, b := base(), base()
	a.PayoutUSD, b.PayoutUSD = 1000, 8000
	pa, pb := personal(a), personal(b)
	if math.Abs(pa-pb) > 1e-12 {
		t.Fatalf("personal factor differs without history: %v vs %v", pa, pb)
	}
	c := base()
	c.ValidReports, c.SubmittedTotal = 6, 10
	if personal(c) <= pa {
		t.Errorf("a good record (%.3f) did not lift the score over the prior (%.3f)", personal(c), pa)
	}
}

func TestGates(t *testing.T) {
	in := base()
	in.InScope = -1
	got := Score(in)
	if got.Gate != GateNoScope || got.Score != 0 {
		t.Errorf("negative scope: gate %q score %.1f", got.Gate, got.Score)
	}
}

// huntqueueEnglish is one reason in English, as the API and CLI render it.
func huntqueueEnglish(r Reason) string {
	out := English([]Reason{r})
	if len(out) == 0 {
		return ""
	}
	return out[0]
}

func TestScoreRangeAndReasons(t *testing.T) {
	for _, in := range []Inputs{base(), {ProgramValue: 100, PayoutUSD: 1e9, MedianDays: 1, InScope: 5000}} {
		res := Score(in)
		if res.Score < 0 || res.Score > 100 {
			t.Errorf("score %.1f outside 0..100", res.Score)
		}
		if len(res.Reasons) == 0 {
			t.Error("no reasons given")
		}
		for _, r := range res.Reasons {
			if r.Key == "" {
				t.Error("empty reason key")
			}
			if len(huntqueueEnglish(r)) == 0 {
				t.Error("a reason renders to nothing")
			}
		}
	}
}
