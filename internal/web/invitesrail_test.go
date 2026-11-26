package web

import (
	"net/http"
	"strings"
	"testing"
)

// TestInvitesWithNothingToFilterShowNoRail pins the empty invites page: a filter
// rail over an empty list is the first thing a reader sees and the last thing they
// need, so it is not rendered until there is something to narrow.
func TestInvitesWithNothingToFilterShowNoRail(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) {})
	res, body := e.do(e.client(), "GET", "/invites?q=zzzznomatch", nil, http.Header{"Accept-Language": {"zh-CN"}})
	if res.StatusCode != 200 {
		t.Fatalf("/invites = %d", res.StatusCode)
	}
	if strings.Contains(body, `class="facets panel"`) {
		t.Error("the empty invites page still shows the filter rail")
	}
	// The programs page keeps it: that list filters.
	_, programs := e.do(e.client(), "GET", "/programs", nil, http.Header{"Accept-Language": {"zh-CN"}})
	if !strings.Contains(programs, `class="facets panel"`) {
		t.Error("the programs page lost its filter rail")
	}
}
