package scanhandoff

import (
	"context"
	"encoding/json"
	"github.com/mdpsec/huntdeck/internal/store"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/scopesentry"
)

// TestReconcileRecordsWhatTheScannerSays pins the last link of the pipeline: a
// handoff's task is read back and its outcome kept, once — a finished task is
// not asked about again — and a task the scanner no longer knows is recorded
// as gone rather than failing the pass.
func TestReconcileRecordsWhatTheScannerSays(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	s := newHandoffStore(t)

	// Two tasks: one the scanner finished, one it forgot.
	if err := s.MarkHandedOff(ctx, []store.ScanCandidate{{Target: "a.example.com", Platform: "bugcrowd", Handle: "opensea"}}, "task-done"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkHandedOff(ctx, []store.ScanCandidate{{Target: "b.example.com", Platform: "bugcrowd", Handle: "opensea"}}, "task-gone"); err != nil {
		t.Fatal(err)
	}

	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var p struct {
			Name string         `json:"name"`
			Args map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		id, _ := p.Args["id"].(string)
		asked = append(asked, id)
		var text string
		if id == "task-done" {
			text = `{"id":"task-done","status":3,"progress":100,"endTime":"2026-05-01 09:00:00"}`
		} else {
			text = `{"isError":true}`
		}
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{"content": []map[string]string{{"type": "text", "text": text}}},
		})
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	client := &scopesentry.Client{BaseURL: srv.URL, Key: "k"}
	sum, err := Reconcile(ctx, s, client, 10, now)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Done != 1 {
		t.Errorf("done = %d, want 1 (%+v)", sum.Done, sum)
	}
	if sum.Gone != 1 {
		t.Errorf("gone = %d, want 1 (%+v)", sum.Gone, sum)
	}

	states, err := s.HandoffStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if states["done"] != 1 || states["gone"] != 1 {
		t.Fatalf("states = %v, want one done and one gone", states)
	}

	// A second pass does not ask about the finished task again.
	before := len(asked)
	if _, err := Reconcile(ctx, s, client, 10, now); err != nil {
		t.Fatal(err)
	}
	for _, id := range asked[before:] {
		if id == "task-done" {
			t.Error("the finished task was asked about twice")
		}
	}
}
