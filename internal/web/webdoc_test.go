package web

import (
	"os"
	"strings"
	"testing"
)

// docs/web-ui.md says it describes each page; the queue page went a month
// without a section there, because nothing checked. This is the same guard the
// API doc has.
func TestWebUIDocMentionsEveryPage(t *testing.T) {
	raw, err := os.ReadFile("../../docs/web-ui.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	pages := []string{"/queue", "/targets", "/programs", "/changes", "/disclosed",
		"/stats", "/invites", "/methodology", "/settings", "/notifications", "/api", "/metrics"}
	for _, p := range pages {
		if !strings.Contains(doc, p) {
			t.Errorf("docs/web-ui.md does not mention %s", p)
		}
	}
}
