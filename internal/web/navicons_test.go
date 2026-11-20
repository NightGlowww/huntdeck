package web

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Five integration links shared one icon - every one drew the same </> glyph, so
// the rail said "these are all the same kind of thing", which is the opposite of
// what a rail is for. This reads the layout and requires each link's icon to
// differ from its neighbours'.
func TestIntegrationLinksHaveDistinctIcons(t *testing.T) {
	b, err := os.ReadFile("templates/layout.html")
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	// Each <a href="/x"...><svg ...><use href="#i-y"/></svg>
	link := regexp.MustCompile(`<a href="(/(?:api|mcp|pojia|scan|llm))"[^>]*>\s*<svg[^>]*>\s*<use href="(#[a-z0-9-]+)"`)
	seen := map[string]string{}
	matches := link.FindAllStringSubmatch(body, -1)
	if len(matches) < 5 {
		t.Fatalf("found %d integration links, want 5: is the pattern looking?", len(matches))
	}
	for _, m := range matches {
		path, icon := m[1], m[2]
		if prev, ok := seen[icon]; ok {
			t.Errorf("%s and %s share the icon %s", prev, path, icon)
		}
		seen[icon] = path
	}
	// And the icon has to exist in the sprite, or the link renders blank.
	for icon := range seen {
		if !strings.Contains(body, `<symbol id="`+strings.TrimPrefix(icon, "#")+`"`) {
			t.Errorf("%s has no symbol in the sprite", icon)
		}
	}
}
