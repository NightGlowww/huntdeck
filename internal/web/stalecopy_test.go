package web

import (
	"net/http"
	"strings"
	"testing"
)

// Two pages naming the same setting have to name it the same way, and the way
// has to be one that works. The model moved to /llm, and both the Model page
// and the polling settings kept pointing at "Accounts > Optional model" and at
// a "Read policies" button on a program page that never existed.
//
// This checks the direction that went stale: nothing may send someone to a
// place the setting no longer lives.
func TestSettingsCopyPointsAtRealPlaces(t *testing.T) {
	e := newTestEnv(t)

	_, llm := e.do(e.client(), http.MethodGet, "/llm", nil, nil)
	for _, stale := range []string{"Accounts › Optional model", "Policy tab"} {
		if strings.Contains(llm, stale) {
			t.Errorf("/llm still points at %q", stale)
		}
	}

	_, set := e.do(e.client(), http.MethodGet, "/settings", nil, nil)
	if strings.Contains(set, "Accounts › Optional model") {
		t.Error("the polling settings still send people to Accounts > Optional model: it is on /llm")
	}
	if !strings.Contains(set, "href=\"/llm\"") {
		t.Error("the polling settings do not link to where the model is configured")
	}
}
