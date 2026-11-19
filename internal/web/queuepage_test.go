package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The queue page renders a built queue: the score's expanding breakdown runs
// only when a row carries factors, which no template-only test reaches.
func TestQueuePageRendersBuiltRows(t *testing.T) {
	e := newTestEnv(t, func(*Deps) {})
	if err := e.store.RefreshHuntQueue(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	res, body := e.do(e.client(), http.MethodGet, "/queue", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("queue page: %d %.200s", res.StatusCode, body)
	}
	if strings.Contains(body, "why-score") {
		if !strings.Contains(body, "Biggest loss") {
			t.Error("a row with factors has no loss line")
		}
	}
}
