package web

import (
	"net/http"
	"strings"
	"testing"
)

// Stats on an install that has polled nothing yet still renders: the page
// shows dashes rather than invented zeroes. (Its no-platforms empty state is
// guarded in the template; a store that has platforms never reaches it.)
func TestStatsOnAnEmptyInstall(t *testing.T) {
	e := newTestEnv(t, func(*Deps) {})
	res, body := e.do(e.client(), http.MethodGet, "/stats", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("stats on an empty install: %d", res.StatusCode)
	}
	if !strings.Contains(body, "By platform") {
		t.Errorf("the stats page has no table: %.200s", body)
	}
}
