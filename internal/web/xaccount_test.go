package web

import (
	"net/http"
	"strings"
	"testing"
)

// A read-only copy links to the X account to follow, in the sidebar and on
// the feedback page, and names it in link previews; a full install never.
func TestXAccount(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) {
		d.ReadOnly, d.XAccount, d.FeedbackEmail, d.PublicURL = true, "huntdeck", "hello@example.com", "https://demo.example.com"
	})
	_, body := e.do(e.client(), http.MethodGet, "/stats", nil, nil)
	for _, want := range []string{`<a class="dn-get" href="https://x.com/huntdeck"`, "Follow @huntdeck", `<meta name="twitter:site" content="@huntdeck">`} {
		if !strings.Contains(body, want) {
			t.Errorf("demo page lacks %s", want)
		}
	}
	_, fb := e.do(e.client(), http.MethodGet, "/feedback", nil, nil)
	if !strings.Contains(fb, "@huntdeck on X") {
		t.Error("feedback page doesn't mention the X account")
	}
	full := newTestEnv(t, func(d *Deps) { d.XAccount = "huntdeck" })
	if _, body := full.do(full.client(), http.MethodGet, "/stats", nil, nil); strings.Contains(body, "x.com/huntdeck") {
		t.Error("a full install links to the X account")
	}
}
