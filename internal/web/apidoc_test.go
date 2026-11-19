package web

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// docs/api.md is what a person reads before writing a script. It went without
// /api/v1/queue for weeks because nothing checked that it covers the routes the
// server actually serves; TestEveryToolIsDocumented has an MCP twin, and this is
// the API's.
func TestAPIDocMentionsEveryRoute(t *testing.T) {
	raw, err := os.ReadFile("../../docs/api.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	brace := regexp.MustCompile(`\{[^}]+\}`)
	s := &server{}
	for _, rt := range s.apiRoutes() {
		// A path with parameters is documented by its concrete shape
		// (/api/v1/programs/hackerone/security), so compare the fixed prefix:
		// the part a reader searches for.
		path := strings.TrimPrefix(rt.Pattern, "GET ")
		path = strings.TrimPrefix(path, "POST ")
		fixed := brace.ReplaceAllString(path, "")
		parts := strings.Split(strings.TrimSuffix(fixed, "/"), "/")
		if len(parts) > 4 {
			parts = parts[:4]
		}
		prefix := strings.Join(parts, "/")
		if !strings.Contains(doc, prefix) {
			t.Errorf("docs/api.md does not mention %s", prefix)
		}
	}
}
