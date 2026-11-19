package web

import (
	"net/http"
	"strings"
	"testing"
)

// Every page in the navigation has to render with the seeded data, not only in
// its empty state: a template branch that runs only with rows once shipped a
// 500. Each case names a marker that proves rows reached the page.
func TestListPagesRenderWithData(t *testing.T) {
	e := newTestEnv(t, func(*Deps) {})
	cases := []struct{ name, path, want string }{
		{"programs", "/programs", "Acme Corp"},
		{"programs bounty", "/programs?bounty=1", "Acme Corp"},
		{"targets", "/targets", "acme.example"},
		{"targets wildcard", "/targets?kind=wildcard", "acme.example"},
		{"targets best", "/targets?sort=best", "targets"},
		{"changes", "/changes", "changes"},
		{"changes scope", "/changes?group=scope", "changes"},
		{"disclosed", "/disclosed", "Open redirect"},
		{"stats", "/stats", "By platform"},
		{"invites", "/invites", "invite"},
		{"queue", "/queue", "Start today"},
		{"methodology", "/methodology", "At a glance"},
		{"api", "/api", "openapi"},
		{"settings", "/settings", "Polling"},
		{"program", "/programs/fake/acme", "Acme Corp"},
		{"program scope", "/programs/fake/acme?tab=scope", "acme.example"},
		{"program policy", "/programs/fake/acme?tab=policy", "Program rules"},
		{"program stats", "/programs/fake/acme?tab=stats", "Acme Corp"},
		{"program changes", "/programs/fake/acme?tab=changes", "changes"},
		{"program notes", "/programs/fake/acme?tab=notes", "notes"},
	}
	for _, c := range cases {
		res, body := e.do(e.client(), http.MethodGet, c.path, nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: %d", c.path, res.StatusCode)
			continue
		}
		if strings.Contains(body, "Something went wrong") || strings.Contains(body, "internal error") {
			t.Errorf("%s rendered an error page", c.path)
		}
		if !strings.Contains(strings.ToLower(body), strings.ToLower(c.want)) {
			t.Errorf("%s lacks %q: rows did not reach the page", c.path, c.want)
		}
	}
}
