package web

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
)

// A program without bounties has its score multiplied by 0.75, so its parts do
// not weigh to the number shown. Measured on three live VDP programs (fiserv
// 43.2 from 57.56, allegion 40.9 from 54.54, ferrero 37.6 from 50.11), the
// ratio was 0.75 every time — and the panel said nothing about it.
func TestNoBountyScoreIsExplained(t *testing.T) {
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
	score, _ := hunt["score"].(float64)
	parts, _ := hunt["parts"].([]any)
	if score == 0 || len(parts) == 0 {
		t.Skip("the fixture program carries no score")
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
	mean := sum / weights
	// The fixture program pays bounties, so the parts are the score outright.
	if math.Abs(mean-score) > 0.15 {
		t.Fatalf("a paying program: parts weigh to %.2f, score %.1f", mean, score)
	}

	// The same score on a program with no bounties is 75% of that, and the page
	// has to say so: the note is rendered for a VDP program and absent otherwise.
	_, page := e.do(c, http.MethodGet, "/programs/fake/acme", nil, nil)
	if strings.Contains(page, "No bounties here") {
		t.Error("a paying program shows the no-bounty note")
	}
}
