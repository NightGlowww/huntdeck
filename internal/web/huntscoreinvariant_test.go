package web

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"
)

// HuntScore is the other decision model, and it is explained the same way: the
// program page shows four parts with their weights. Those weights are a
// weighted mean, not a product — measured on the live instance, 7.7x0.35 +
// 67.6x0.30 + 91.6x0.25 + 52.2x0.10 = 51.09 for a score of 51.1 — and a test
// that assumes the queue's arithmetic would be wrong here.
func TestHuntScorePartsAreAWeightedMean(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	res, body := e.do(c, http.MethodGet, "/api/v1/programs/fake/acme", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("program: %d", res.StatusCode)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	hunt, _ := payload["hunt_score"].(map[string]any)
	score, ok := hunt["score"].(float64)
	if !ok || score == 0 {
		t.Skip("the fixture program carries no score")
	}
	parts, _ := hunt["parts"].([]any)
	if len(parts) == 0 {
		t.Skip("the fixture score has no parts")
	}
	sum, weights := 0.0, 0.0
	for _, p := range parts {
		m, ok := p.(map[string]any)
		if !ok {
			continue
		}
		v, _ := m["score"].(float64)
		w, _ := m["weight"].(float64)
		sum += v * w
		weights += w
	}
	if math.Abs(weights-1) > 0.01 {
		t.Fatalf("the weights add to %.3f, not 1", weights)
	}
	if math.Abs(sum-score) > 0.15 {
		t.Errorf("the parts weigh to %.2f but the score says %.1f", sum, score)
	}
}
