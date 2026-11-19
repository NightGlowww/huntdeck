package huntqueue

import (
	"math"
	"testing"
)

// Readiness falls with what makes a target hard to start today: credentials to
// request, hand testing, and a program that keeps flipping.
func TestReadinessFollowsTheFlags(t *testing.T) {
	base := Inputs{ProgramValue: 60, PayoutUSD: 5000, MedianDays: 20, InScope: 50, Disclosed365: 5}
	open := Score(base)
	creds := base
	creds.NeedsCreds = true
	noAuto := base
	noAuto.NoAutomation = true
	flip := base
	flip.Flips = 5

	if !(Score(creds).Score < open.Score) {
		t.Errorf("credentials did not lower the score: %v vs %v", Score(creds).Score, open.Score)
	}
	if !(Score(noAuto).Score < Score(creds).Score) {
		t.Errorf("hand testing is not worse than credentials: %v vs %v", Score(noAuto).Score, Score(creds).Score)
	}
	if !(Score(flip).Score < open.Score) {
		t.Errorf("flips did not lower the score: %v vs %v", Score(flip).Score, open.Score)
	}
	// A small number of flips is ordinary and must not move the score.
	quiet := base
	quiet.Flips = 1
	if math.Abs(Score(quiet).Score-open.Score) > 0.001 {
		t.Errorf("one flip changed the score: %v vs %v", Score(quiet).Score, open.Score)
	}
}
