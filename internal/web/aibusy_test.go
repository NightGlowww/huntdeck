package web

import (
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/ai"
)

// Reading three policies takes up to a minute. The button has to say so and
// stay busy: the default eight-second reset would offer a click that lands on a
// request already in flight.
func TestExtractButtonStaysBusy(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) {
		d.Password = "secret"
		d.AIBaseURL, d.AIModel = "http://example.invalid/v1", "fake"
		d.AI = &ai.Client{BaseURL: "http://example.invalid/v1", Model: "fake"}
	})
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if r, _ := e.do(c, http.MethodPost, "/login", urlValues("secret"), nil); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", r.StatusCode)
	}
	_, body := e.do(c, http.MethodGet, "/settings", nil, nil)
	if !strings.Contains(body, "data-stay-busy") {
		t.Error("the extract button does not stay busy")
	}
	if !strings.Contains(body, "Reading") {
		t.Error("the extract button does not say what it is doing")
	}
}
