package web

import (
	"net/http"
	"strings"
	"testing"
)

// TestOwnProgramHeadingIsTranslated pins the independent-program form's heading:
// it came from a Go method and went through {{T}} with a phrase the catalog did not
// have, so a Chinese page opened on <h1>Add a program</h1>.
func TestOwnProgramHeadingIsTranslated(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) {})
	_, body := e.do(e.client(), "GET", "/programs/independent/new", nil, http.Header{"Accept-Language": {"zh-CN"}})
	if !strings.Contains(body, "添加项目") {
		t.Error("the heading is not Chinese")
	}
	if strings.Contains(body, "m-heading\">Add a program") {
		t.Error("the heading still prints Add a program")
	}
}
