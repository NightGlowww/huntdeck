package web

import (
	"net/http"
	"strings"
	"testing"
)

// scripts/smoke.sh decides a page is the right page by looking for the body
// attribute data-page. That contract lives in two files, so it is pinned here:
// a renamed Nav value would turn the smoke run into a wall of false failures at
// deploy time, and two pages sharing a key would make the check blind.
func TestEverySmokeMarkerIsItsOwnPage(t *testing.T) {
	pages := []struct {
		path, marker string
	}{
		{"/", "dashboard"},
		{"/queue", "queue"},
		{"/programs", "programs"},
		{"/targets", "targets"},
		{"/disclosed", "disclosed"},
		{"/changes", "changes"},
		{"/settings", "settings"},
		{"/api", "api"},
	}
	seen := map[string]string{}
	e := newTestEnv(t, func(*Deps) {})
	c := e.client()
	for _, p := range pages {
		if other, ok := seen[p.marker]; ok {
			t.Errorf("%s and %s share the marker %q: smoke could not tell them apart", other, p.path, p.marker)
		}
		seen[p.marker] = p.path
		res, body := e.do(c, http.MethodGet, p.path, nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: %d", p.path, res.StatusCode)
			continue
		}
		want := "data-page=\"" + p.marker + "\""
		if !strings.Contains(body, want) {
			t.Errorf("%s does not carry %s; smoke would report a false failure", p.path, want)
		}
	}
}
