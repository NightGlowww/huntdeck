package web

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every route the API declares is described in docs/api.md. The document shows
// routes by example (a concrete /programs/hackerone/security) rather than by
// shape, so a route's parameters are matched as "one path segment".
//
// The first version of this guard cut the path to four segments, which let a
// new nested route match the already documented /api/v1/programs and pass
// without being written down. Six routes were missing when that was fixed.
func TestAPIDocMentionsEveryRoute(t *testing.T) {
	raw, err := os.ReadFile("../../docs/api.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	brace := regexp.MustCompile(`\{[^}]+\}`)
	s := &server{}
	checked := 0
	for _, rt := range s.apiRoutes() {
		path := strings.TrimPrefix(rt.Pattern, "GET ")
		path = strings.TrimPrefix(path, "POST ")
		path = strings.TrimSuffix(path, "/")
		if path == "" || path == "/" {
			continue
		}
		// A parameter is any one segment; everything else is literal.
		pattern := regexp.QuoteMeta(brace.ReplaceAllString(path, "*"))
		pattern = strings.ReplaceAll(pattern, "\\*", `[^/"]+`)
		if !regexp.MustCompile(pattern).MatchString(doc) {
			t.Errorf("docs/api.md does not describe %s", path)
		}
		checked++
	}
	if checked < 20 {
		t.Fatalf("only %d routes checked: the guard is looking at nothing", checked)
	}
	t.Logf("checked %d routes", checked)
}
