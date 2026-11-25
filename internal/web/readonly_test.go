package web

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/overrides"
)

// A read-only copy serves the platform data but nothing that changes
// anything, and none of the user's own pages.
func TestReadOnly(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) { d.ReadOnly = true })
	c := e.client()
	tests := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/", http.StatusOK},
		{http.MethodGet, "/programs", http.StatusOK},
		{http.MethodGet, "/programs/fake/acme", http.StatusOK},
		{http.MethodGet, "/stats", http.StatusOK},
		{http.MethodGet, "/api/v1/programs", http.StatusOK},
		{http.MethodPost, "/api/v1/match", http.StatusOK},
		{http.MethodGet, "/settings", http.StatusOK},
		{http.MethodGet, "/settings?tab=accounts", http.StatusOK},
		{http.MethodGet, "/settings?tab=browser", http.StatusOK},
		{http.MethodGet, "/settings/notifications", http.StatusOK},
		{http.MethodGet, "/settings/notifications/new", http.StatusOK},
		{http.MethodGet, "/invites", http.StatusOK},
		{http.MethodGet, "/api/v1/invites", http.StatusNotFound},
		{http.MethodGet, "/novnc/", http.StatusNotFound},
		{http.MethodGet, "/settings/restart", http.StatusNotFound},
		{http.MethodPost, "/settings/accounts", http.StatusForbidden},
		{http.MethodPost, "/settings/restart", http.StatusNotFound},
		{http.MethodPost, "/settings/notifications", http.StatusForbidden},
		{http.MethodPost, "/invites/fake/acme/accept", http.StatusForbidden},
		{http.MethodPost, "/programs/fake/acme/ignore", http.StatusForbidden},
		{http.MethodPost, "/programs/fake/acme/tags", http.StatusForbidden},
		{http.MethodPost, "/api/v1/poll/fake", http.StatusForbidden},
		{http.MethodPut, "/programs", http.StatusForbidden},
	}
	for _, tt := range tests {
		var form url.Values
		if tt.method == http.MethodPost {
			form = url.Values{"tags": {"x"}, "ignored": {"1"}, "q": {"app.acme.example"}}
		}
		res, body := e.do(c, tt.method, tt.path, form, nil)
		if res.StatusCode != tt.want {
			t.Errorf("%s %s = %d, want %d", tt.method, tt.path, res.StatusCode, tt.want)
		}
		if tt.want == http.StatusForbidden && !strings.Contains(body, "read-only") {
			t.Errorf("%s %s: %q doesn't say it's read-only", tt.method, tt.path, body)
		}
	}
	// Pages show everything a full install does, the user's own pages
	// empty, with a note on those; and say it's a demo.
	for _, path := range []string{"/", "/programs/fake/acme", "/stats", "/settings", "/invites", "/settings/notifications"} {
		_, body := e.do(c, http.MethodGet, path, nil, nil)
		for _, want := range []string{`href="/settings"`, `href="/invites"`, `href="/settings/notifications"`, "<b>Public</b>", "<b>HuntDeck Public</b>", "Every public program on all four platforms, ", `<html lang="en" data-read-only>`,
			`<div class="demo-banner" role="note">`, `name="tm-palette" value="nord"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %s", path, want)
			}
		}
		if strings.Contains(body, "Self-host HuntDeck") {
			t.Errorf("%s links to a source repository with none set", path)
		}
		if tour := strings.Contains(body, `<p class="demo-tour" role="note">`); tour != (path == "/settings" || path == "/invites" || path == "/settings/notifications") {
			t.Errorf("%s: demo note %v", path, tour)
		}
	}
	// Settings › Email's old addresses go to the HuntMail tour: there's no
	// Setup tab here.
	for _, old := range []string{"/settings?tab=email", "/settings/huntmail"} {
		if res, _ := e.do(c, http.MethodGet, old, nil, nil); res.StatusCode != http.StatusMovedPermanently || res.Header.Get("Location") != "/email/stats" {
			t.Errorf("%s: %d to %q", old, res.StatusCode, res.Header.Get("Location"))
		}
	}
	// Invites is a tour: a picture with made-up invites, no list.
	if _, body := e.do(c, http.MethodGet, "/invites", nil, nil); !strings.Contains(body, "tour/invites.webp") || !strings.Contains(body, "made-up invites") || strings.Contains(body, "<table") {
		t.Error("Invites isn't a tour with made-up invites")
	}
	if _, body := e.do(c, http.MethodGet, "/settings?tab=browser", nil, nil); !strings.Contains(body, `class="pf pf-fake"`) {
		t.Error("the Browser tab doesn't list the platforms")
	}
	if _, body := e.do(c, http.MethodGet, "/stats?visibility=private", nil, nil); !strings.Contains(body, "Private shows the stats of the private programs") {
		t.Error("private stats lack their demo note")
	}
	for path, want := range map[string]string{"/": "Private invites", "/programs/fake/acme": "/tags\"", "/stats": `class="seg vis-toggle"`} {
		if _, body := e.do(c, http.MethodGet, path, nil, nil); !strings.Contains(body, want) {
			t.Errorf("%s lacks %s", path, want)
		}
	}
	// Nothing was changed by the refused requests.
	if _, body := e.do(c, http.MethodGet, "/programs/fake/acme", nil, nil); strings.Contains(body, ">x<") {
		t.Error("a tag was saved in a read-only copy")
	}
}

// A read-only copy's Settings show every account field empty, even when
// it's handed the account's settings: nothing of them reaches a page.
func TestReadOnlyAccountsEmpty(t *testing.T) {
	env := map[string]string{"H1_USERNAME": "hunter-handle", "IT_TOKEN": "secret-token", "BC_PUBLIC_ONLY": "true"}
	src := config.Source{
		LookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		ReadFile:  func(string) ([]byte, error) { return nil, os.ErrNotExist },
	}
	e := newTestEnv(t, func(d *Deps) {
		d.ReadOnly = true
		d.Accounts, d.Restart = overrides.New(d.Store, t.TempDir(), src), &fakeRestarter{}
	})
	_, body := e.do(e.client(), http.MethodGet, "/settings?tab=accounts", nil, nil)
	for _, want := range []string{"API username", "API token", `name="H1_USERNAME"`} {
		if !strings.Contains(body, want) {
			t.Errorf("accounts tab lacks %s", want)
		}
	}
	for _, leak := range []string{"hunter-handle", "secret-token", `title="Set in your environment (.env)"`} {
		if strings.Contains(body, leak) {
			t.Errorf("accounts tab shows %q", leak)
		}
	}
}

// A read-only copy's API page lists what it serves, and says how to call
// it from a script.
func TestReadOnlyAPIPage(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) { d.ReadOnly = true })
	_, body := e.do(e.client(), http.MethodGet, "/api", nil, nil)
	for _, want := range []string{"get a challenge from a terminal", `<code>/api/v1/programs</code>`, `<span class="mth post">POST</span><code>/api/v1/match</code>`} {
		if !strings.Contains(body, want) {
			t.Errorf("read-only API page lacks %s", want)
		}
	}
	for _, not := range []string{"private network", `<code>/api/v1/poll/{platform}</code>`, `<code>/api/v1/invites`} {
		if strings.Contains(body, not) {
			t.Errorf("read-only API page has %s", not)
		}
	}
	_, body = newTestEnv(t).do(e.client(), http.MethodGet, "/api", nil, nil)
	if !strings.Contains(body, `<code>/api/v1/poll/{platform}</code>`) {
		t.Error("the full API page lacks the poll endpoint")
	}
}
