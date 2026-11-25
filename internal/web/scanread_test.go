package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/store"
)

// TestReadNowAsksTheScanner pins the button that saves a minute: pressing it runs
// the same pass serve runs, and the page says what came back. Without it a reader
// who had just started a task waited for the next tick to see it running.
func TestReadNowAsksTheScanner(t *testing.T) {
	ctx := t.Context()
	var asked int
	scanner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{"content": []map[string]string{{
				"type": "text",
				"text": `{"id":"task-1","status":3,"progress":100}`,
			}}},
		})
		_, _ = w.Write(body)
	}))
	defer scanner.Close()

	e := newTestEnv(t, func(d *Deps) {
		d.ScanURL = scanner.URL
		d.ScanKey = config.Secret("ssk_test")
	})
	if err := e.store.MarkHandedOff(ctx, []store.ScanCandidate{{Target: "a.example.com", Platform: "bugcrowd", Handle: "opensea"}}, "task-1"); err != nil {
		t.Fatal(err)
	}

	res, _ := e.do(e.client(), "POST", "/scan/read", nil, nil)
	if res.StatusCode != 303 {
		t.Fatalf("read now = %d, want a redirect", res.StatusCode)
	}
	if asked == 0 {
		t.Fatal("the scanner was never asked")
	}
	res, body := e.do(e.client(), "GET", res.Header.Get("Location"), nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("page after read = %d", res.StatusCode)
	}
	if !strings.Contains(body, "read 1 task(s): 1 finished, 0 failed") {
		t.Errorf("the page does not say what came back")
	}
	states, err := e.store.HandoffStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if states["done"] != 1 {
		t.Errorf("states = %v, want the task kept as done", states)
	}
}
