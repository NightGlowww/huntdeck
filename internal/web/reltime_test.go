package web

import (
	"net/http"
	"regexp"
	"testing"
)

// A relative time the server prints once freezes on an open page. Every one of
// them has to carry data-at, or the ticker cannot find it (the top bar and the
// queue's "Built" line each froze for a while before this was noticed).
func TestRelativeTimesCarryTheirTimestamp(t *testing.T) {
	e := newTestEnv(t, func(*Deps) {})
	when := regexp.MustCompile(`<span class="when[^"]*"(?P<attrs>[^>]*)>([0-9]+[smhd] ago|just now)</span>`)
	for _, path := range []string{"/changes", "/programs"} {
		_, body := e.do(e.client(), http.MethodGet, path, nil, nil)
		for _, m := range when.FindAllStringSubmatch(body, -1) {
			if !regexp.MustCompile(`data-at=`).MatchString(m[0]) {
				t.Errorf("%s: a relative time without data-at: %s", path, m[0])
			}
		}
	}
}
