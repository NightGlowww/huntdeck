package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A script presents a bearer token instead of a login cookie: one key to the
// same gate. A live token passes, a wrong or revoked one does not, and a
// browser session keeps working alongside it.
func TestBearerTokenPassesTheGate(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) { d.Password = "secret" })
	token := "tok-abc-123"
	if _, err := e.store.CreateAPIToken(context.Background(), "test", token); err != nil {
		t.Fatal(err)
	}
	get := func(auth string) int {
		h := http.Header{}
		if auth != "" {
			h.Set("Authorization", auth)
		}
		res, _ := e.do(http.DefaultClient, http.MethodGet, "/api/v1/status", nil, h)
		return res.StatusCode
	}
	if code := get("Bearer " + token); code != http.StatusOK {
		t.Fatalf("a live token must pass: %d", code)
	}
	if code := get("Bearer nope"); code != http.StatusUnauthorized {
		t.Fatalf("a wrong token must be 401: %d", code)
	}
	list, err := e.store.APITokens(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	if err := e.store.RevokeAPIToken(context.Background(), list[0].ID); err != nil {
		t.Fatal(err)
	}
	if code := get("Bearer " + token); code != http.StatusUnauthorized {
		t.Fatalf("a revoked token must be 401: %d", code)
	}
	// The browser path is untouched: the login form still lands somewhere.
	form := url.Values{"password": {"secret"}, "next": {"/"}}
	c := e.client()
	res, _ := e.do(c, http.MethodPost, "/login", form, nil)
	if res.StatusCode != http.StatusSeeOther || !strings.Contains(res.Header.Get("Location"), "/") {
		t.Fatalf("login: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
}
