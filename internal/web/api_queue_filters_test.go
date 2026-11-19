package web

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// The queue API used to answer a different question than the page: the chips'
// filters were accepted and ignored. These pin that they now apply, and that a
// boolean written the way a script writes it is understood.
func TestQueueAPIAppliesTheChips(t *testing.T) {
	e := newTestEnv(t, func(*Deps) {})
	if err := e.store.RefreshHuntQueue(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	get := func(q string) (int, apiQueueJSON) {
		res, body := e.do(e.client(), http.MethodGet, "/api/v1/queue?"+q, nil, nil)
		var out apiQueueJSON
		if res.StatusCode == http.StatusOK {
			if err := json.Unmarshal([]byte(body), &out); err != nil {
				t.Fatal(err)
			}
		}
		return res.StatusCode, out
	}

	code, all := get("limit=50")
	if code != http.StatusOK || len(all.Rows) == 0 {
		t.Fatalf("unfiltered queue: %d, %d rows", code, len(all.Rows))
	}
	// one=1 keeps one row per program.
	_, one := get("limit=50&one=true")
	seen := map[string]bool{}
	for _, r := range one.Rows {
		key := r.Platform + "/" + r.Handle
		if seen[key] {
			t.Errorf("%s appears twice under one=1", key)
		}
		seen[key] = true
	}
	if len(one.Rows) > len(all.Rows) {
		t.Errorf("one=1 returned more rows (%d) than unfiltered (%d)", len(one.Rows), len(all.Rows))
	}
	// A window that does not parse is a 400, not a silently ignored filter.
	if code, _ := get("added=abc"); code != http.StatusBadRequest {
		t.Errorf("added=abc: %d, want 400", code)
	}
}
