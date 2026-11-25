package web

import (
	"net/http"
	"strings"
	"testing"
)

// TestVisToggleLabelsAreTranslated pins the Stats pages' All / Public / Private
// toggle: its tooltips went through the catalog from the start, its visible
// labels did not, so a Chinese page showed "All 312 / Public 88 / Private 224".
// Same shape as the queue chips: copy held in Go, printed raw.
//
// It asks for Chinese: the first version of this test read the English page and
// could not tell a translated label from a raw one.
func TestVisToggleLabelsAreTranslated(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) {})
	res, body := e.do(e.client(), "GET", "/stats", nil, http.Header{"Accept-Language": {"zh-CN"}})
	if res.StatusCode != 200 {
		t.Fatalf("/stats = %d", res.StatusCode)
	}
	if !strings.Contains(body, "vis-toggle") {
		t.Skip("the toggle is not on this page in the fixture")
	}
	for _, raw := range []string{">All <", ">Public <", ">Private <"} {
		if strings.Contains(body, raw) {
			t.Errorf("the toggle still prints %q on a Chinese page", raw)
		}
	}
	if !strings.Contains(body, "公开") {
		t.Error("the toggle does not say 公开")
	}
}
