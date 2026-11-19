package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The queue page's chips, its one way out, and how far the backtest has to go
// are all things a reader relies on; each was checked by hand first.
func TestQueuePageChipsAndSnapshotFooter(t *testing.T) {
	e := newTestEnv(t, func(*Deps) {})
	if err := e.store.RefreshHuntQueue(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}

	_, plain := e.do(e.client(), http.MethodGet, "/queue", nil, nil)
	if strings.Contains(plain, "Clear filters") {
		t.Error("the clear chip shows with no filters set")
	}
	if !strings.Contains(plain, "snapshots") {
		t.Error("the page does not say how far the backtest has to go")
	}

	_, filtered := e.do(e.client(), http.MethodGet, "/queue?paying=1&one=1", nil, nil)
	if !strings.Contains(filtered, "Clear filters") {
		t.Error("no way out of a filtered queue")
	}
	if !strings.Contains(filtered, `class="chip clear"`) {
		t.Error("the clear chip does not carry its own style")
	}
}
