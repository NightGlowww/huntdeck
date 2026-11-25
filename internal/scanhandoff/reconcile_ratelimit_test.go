package scanhandoff

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/scopesentry"
	"github.com/mdpsec/huntdeck/internal/store"
)

// TestReconcileStopsOnRateLimit pins what a 429 means for the pass: stop, keep
// the tasks already read, leave the rest for the next one, and do not call it a
// failure. The scanner answered 429 to a real reconcile run, which is why most
// handoffs still showed "handed over, state unread".
func TestReconcileStopsOnRateLimit(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	s := newHandoffStore(t)
	for _, id := range []string{"task-a", "task-b"} {
		if err := s.MarkHandedOff(ctx, []store.ScanCandidate{{Target: id + ".example.com", Platform: "bugcrowd", Handle: "opensea"}}, id); err != nil {
			t.Fatal(err)
		}
	}

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{"content": []map[string]string{{
				"type": "text",
				"text": `{"id":"task-a","status":3,"progress":100}`,
			}}},
		})
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	client := &scopesentry.Client{BaseURL: srv.URL, Key: "k"}
	sum, err := Reconcile(ctx, s, client, 10, now)
	if err != nil {
		t.Fatalf("a rate limit is not a failure: %v", err)
	}
	if !sum.RateLimited {
		t.Error("the summary does not say the pass was cut short")
	}
	if sum.Read != 1 || sum.Done != 1 {
		t.Errorf("read %d, done %d; want the one task that answered", sum.Read, sum.Done)
	}
	states, err := s.HandoffStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if states["done"] != 1 {
		t.Errorf("states = %v, want the read task kept as done", states)
	}
	if _, unread := states[""]; !unread {
		t.Errorf("states = %v, want the second task still unread", states)
	}
}
