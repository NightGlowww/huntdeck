package web

import (
	"net/http"
	"strings"
	"testing"
)

// TestChangeDetailHeadingIsTranslated pins the change detail's <h1>: the label is a
// field name from ruleLabels ("Policy", "Reward notes"…), and the page printed it
// as it is, so a Chinese reader got 已编辑：Policy.
func TestChangeDetailHeadingIsTranslated(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) {})
	_, list := e.do(e.client(), "GET", "/changes", nil, http.Header{"Accept-Language": {"zh-CN"}})
	i := strings.Index(list, "/changes/")
	if i < 0 {
		t.Skip("no changes in the fixture")
	}
	rest := list[i+len("/changes/"):]
	id := rest[:strings.IndexAny(rest, "\"> ")]
	if id == "" {
		t.Skip("no change id in the list")
	}
	_, body := e.do(e.client(), "GET", "/changes/"+id, nil, http.Header{"Accept-Language": {"zh-CN"}})
	for _, raw := range []string{"：Policy<", "：Reward notes<", "：Program exclusions<"} {
		if strings.Contains(body, raw) {
			t.Errorf("the heading prints %q untranslated", raw)
		}
	}
}
