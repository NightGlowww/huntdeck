package web

import (
	"net/http"
	"strings"
	"testing"
)

// A crawler that gets a login redirect reads the login page and never sees
// "Disallow: /". robots.txt exists to be read without a session; the sitemap
// and security.txt may 404 on a private instance, but they must not bounce to
// the login page either.
func TestCrawlerFilesAreNotBehindTheLogin(t *testing.T) {
	e := newTestEnv(t, func(*Deps) {})
	c := e.client()
	res, robots := e.do(c, http.MethodGet, "/robots.txt", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/robots.txt: %d, want 200 without a session", res.StatusCode)
	}
	if !strings.Contains(robots, "Disallow: /") {
		t.Errorf("robots.txt does not disallow the site: %.120s", robots)
	}
	for _, path := range []string{"/sitemap.xml", "/.well-known/security.txt"} {
		res, _ := e.do(c, http.MethodGet, path, nil, nil)
		if res.StatusCode == http.StatusSeeOther && strings.Contains(res.Header.Get("Location"), "/login") {
			t.Errorf("%s bounces to the login page", path)
		}
	}
}
