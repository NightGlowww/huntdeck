package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// An agent reads the API, not the page: an unreadable table must not come back
// as an empty 200, which reads as "there is nothing" and quietly ends the work.
// The page-level version of this is in fault_test.go.
func TestAPIReportsAStoreFailureAs500(t *testing.T) {
	e := newTestEnv(t)
	if _, err := e.db.W.ExecContext(context.Background(), "ALTER TABLE hunt_queue RENAME TO hunt_queue_gone"); err != nil {
		t.Fatal(err)
	}
	res, body := e.do(e.client(), http.MethodGet, "/api/v1/queue?limit=5", nil, nil)
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("queue with its table gone = %d, want 500; body %.200s", res.StatusCode, body)
	}
	if !strings.Contains(body, `"error"`) {
		t.Errorf("a 500 should carry an error object: %.200s", body)
	}
}
