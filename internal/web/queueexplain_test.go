package web

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"testing"
)

// The queue shows its work: each row carries the factors that produced it, and
// the page, the CLI and an agent all present them as the reason for the number.
// That is only honest if the factors multiply back to the score — otherwise the
// explanation is decoration. Measured on the live instance first: 0.804 × 0.971 ×
// 0.9 = 0.703 for the top row's 70.3.
func TestQueueFactorsMultiplyToTheScore(t *testing.T) {
	e := newTestEnv(t)
	// The queue is derived data: the fixture has programs and scope, and the
	// rebuild is what the server runs every half hour.
	if err := e.store.RefreshHuntQueue(context.Background(), e.store.Now()); err != nil {
		t.Fatal(err)
	}
	c := e.client()
	res, body := e.do(c, http.MethodGet, "/api/v1/queue?explain=1&limit=5", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("queue: %d", res.StatusCode)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	rows, _ := payload["rows"].([]any)
	if len(rows) == 0 {
		t.Skip("the fixture has no queue rows")
	}
	for _, row := range rows {
		r, ok := row.(map[string]any)
		if !ok {
			continue
		}
		score, _ := r["score"].(float64)
		factors, _ := r["factors"].([]any)
		if len(factors) == 0 {
			t.Fatalf("a row with score %.1f carries no factors", score)
		}
		product := 1.0
		for _, f := range factors {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			if effect, ok := fm["effect"].(float64); ok {
				product *= effect
			}
		}
		if math.Abs(product*100-score) > 0.15 {
			t.Errorf("factors multiply to %.3f (%.1f) but the row says %.1f", product, product*100, score)
		}
	}
}
