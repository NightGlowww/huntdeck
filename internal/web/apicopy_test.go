package web

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

// urlValues is the login form as the page posts it.
func urlValues(password string) url.Values {
	return url.Values{"password": {password}, "next": {"/"}}
}

// Twice a feature shipped and the page kept describing the world before it: the
// API page still said "no token" after tokens existed. The copy is part of the
// change, so it is checked like one.
func TestAPIPageCopyMatchesTheGate(t *testing.T) {
	withPassword := newTestEnv(t, func(d *Deps) { d.Password = "secret" })
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if r, _ := withPassword.do(c, http.MethodPost, "/login", urlValues("secret"), nil); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", r.StatusCode)
	}
	res, body := withPassword.do(c, http.MethodGet, "/api", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("api page: %d", res.StatusCode)
	}
	if strings.Contains(body, "no API tokens") {
		t.Error("a password instance claims there are no tokens")
	}
	if !strings.Contains(body, "Bearer") {
		t.Error("the tokens panel is missing on a password instance")
	}

	open := newTestEnv(t, func(*Deps) {})
	_, openBody := open.do(open.client(), http.MethodGet, "/api", nil, nil)
	if !strings.Contains(openBody, "nothing for a token to unlock") {
		t.Error("an instance without a password offers tokens that cannot work")
	}
}
