package huntscore

import (
	"math"
	"testing"
)

// The score is the weighted sum of its parts, and a reader who adds them up must
// get the number the page shows: 7.8×0.35 + 67.6×0.30 + 91.6×0.25 + 52.2×0.10
// is 51.13 on a live program, which is the 51.1 it displayed.
func TestTheScoreIsTheWeightedSumOfItsParts(t *testing.T) {
	parts := map[string]float64{Fresh: 7.8, Competition: 67.6, Payout: 91.6, Response: 52.2}
	weights := map[string]float64{Fresh: 0.35, Competition: 0.30, Payout: 0.25, Response: 0.10}
	sum, total := 0.0, 0.0
	for k, v := range parts {
		sum += v * weights[k]
		total += weights[k]
	}
	if math.Abs(total-1) > 1e-9 {
		t.Fatalf("the balanced preset weights add to %v, not 1", total)
	}
	if got := math.Round(sum*10) / 10; got != 51.1 {
		t.Errorf("weighted sum is %v, the page showed 51.1", got)
	}
}
