package web

import (
	"strings"
	"testing"
)

// POJIA answers are text with report ids in them; the page turns each into a
// link. The guard from the AGENTS rule: a linkifier that escaped its own
// markup, or let POJIA's text through as HTML, would fail here.
func TestPojiaLinksEscapesAndLinks(t *testing.T) {
	out := string(pojiaLinks("#123456 <script>alert(1)</script> plain"))
	if !strings.Contains(out, `href="/pojia/report?id=123456">#123456</a>`) {
		t.Errorf("the report id is not a link: %s", out)
	}
	if strings.Contains(out, "<script>") {
		t.Error("markup in the answer reached the page unescaped")
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Error("the escaped form is missing")
	}
}
