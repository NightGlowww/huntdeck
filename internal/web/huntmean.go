package web

import (
	"github.com/mdpsec/huntdeck/internal/store"
)

// huntMean is the weighted mean of a score's parts: what the four numbers weigh
// to before any scaling. A program without bounties has its score multiplied by
// 0.75 (huntscore.vdpFactor), and without this the panel showed parts that did
// not add up to the number above them.
func huntMean(h store.HuntDetail) float64 {
	var sum, total float64
	for _, p := range h.Parts {
		sum += p.Score * p.Weight
		total += p.Weight
	}
	if total == 0 {
		return 0
	}
	return sum / total
}
