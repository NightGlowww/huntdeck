package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/huntmail/setup"
	"github.com/mdpsec/huntdeck/internal/overrides"
)

const setupToken = "cf-token-for-the-test-0123"

// okCloudflare answers every call as a fresh account would: no Worker yet,
// the rest succeeds. refuse makes it refuse the token.
func okCloudflare(t *testing.T, refuse bool) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		reply := func(status int, v any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(v)
		}
		if refuse || r.Header.Get("Authorization") != "Bearer "+setupToken {
			reply(http.StatusForbidden, map[string]any{"success": false, "errors": []any{map[string]any{"code": 10000, "message": "Authentication error"}}})
			return
		}
		var result any = map[string]any{}
		p := r.URL.Path
		switch {
		case p == "/zones":
			result = []any{map[string]any{"id": "z1", "name": r.URL.Query().Get("name"), "account": map[string]any{"id": "a1"}}}
		case strings.HasSuffix(p, "/settings"):
			reply(http.StatusNotFound, map[string]any{"success": false, "errors": []any{map[string]any{"code": 10007, "message": "not found"}}})
			return
		case strings.HasSuffix(p, "/workers/subdomain"):
			result = map[string]any{"subdomain": "hunter"}
		case strings.HasSuffix(p, "/d1/database") && r.Method == http.MethodGet:
			result = []any{}
		case strings.HasSuffix(p, "/d1/database"):
			result = map[string]any{"uuid": "db1"}
		case strings.HasSuffix(p, "/query"):
			result = []any{map[string]any{"results": []any{}, "success": true}}
		}
		reply(http.StatusOK, map[string]any{"success": true, "errors": []any{}, "result": result})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func setupEnv(t *testing.T, refuse bool) (*testEnv, *overrides.Saved) {
	b, err := setup.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	api := okCloudflare(t, refuse)
	health := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(health.Close)
	hc := httpclient.New(httpclient.Options{RatePerSecond: 1000, Burst: 1000, MaxAttempts: 1})
	src := config.Source{LookupEnv: func(string) (string, bool) { return "", false }, ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist }}
	var saved *overrides.Saved
	e := newTestEnv(t, func(d *Deps) {
		saved = overrides.New(d.Store, t.TempDir(), src)
		d.MailBundle, d.Accounts = b, saved
		d.MailSetupTest = func(o *setup.Options) {
			o.API, o.HTTP, o.Health, o.HealthAt = api.URL, hc, hc, health.URL
			o.Sleep = func(context.Context, time.Duration) error { return nil }
		}
	})
	return e, saved
}

// waitSetup waits for the run to end and returns the page.
func waitSetup(t *testing.T, e *testEnv) string {
	t.Helper()
	c := e.client()
	for range 200 {
		_, body := e.do(c, "GET", "/email/setup/status", nil, nil)
		if strings.Contains(body, `"running":false`) {
			_, page := e.do(c, "GET", "/email/setup", nil, nil)
			return page
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the run didn't end")
	return ""
}

func TestMailSetup(t *testing.T) {
	e, saved := setupEnv(t, false)
	c := e.client()
	_, body := e.do(c, "GET", "/email/setup", nil, nil)
	for _, want := range []string{`action="/email/setup"`, `name="token"`, "Email Routing Rules: Edit", ">Set up HuntMail</button>",
		`id="acct-huntmail"`, `name="HUNTMAIL_API_KEY"`, `<a href="/email/setup" class="on" aria-current="page">Setup</a>`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if _, body := e.do(c, "GET", "/email", nil, nil); !strings.Contains(body, `href="/email/setup#msetup-form"`) {
		t.Error("the Email page doesn't offer the setup")
	}
	for form, want := range map[string]string{
		"domains=example.com":                     "Enter a Cloudflare API token",
		"token=x&domains=":                        "Enter the domain",
		"token=x&domains=not+a+domain%2Fx":        "isn&#39;t a domain name",
		"token=x&domains=example.com&name=Bad+Na": "lowercase letters",
	} {
		vals, _ := url.ParseQuery(form)
		res, body := e.do(c, "POST", "/email/setup", vals, nil)
		if form == "token=x&domains=example.com&name=Bad+Na" { // the run checks the name
			body = waitSetup(t, e)
		} else if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d", form, res.StatusCode)
		}
		if !strings.Contains(body, want) {
			t.Errorf("%s: no %q", form, want)
		}
	}

	res, _ := e.do(c, "POST", "/email/setup", url.Values{"token": {setupToken}, "domains": {"Example.com"}}, nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("start: %d", res.StatusCode)
	}
	page := waitSetup(t, e)
	if !strings.Contains(page, "<h2>Done</h2>") || !strings.Contains(page, "https://huntmail.hunter.workers.dev") || strings.Contains(page, setupToken) {
		t.Fatalf("the finished run:\n%s", page)
	}
	if _, st := e.do(c, "GET", "/email/setup/status", nil, nil); !strings.Contains(st, `"saved":true`) {
		t.Errorf("status: %s", st)
	}
	cfg, err := saved.Config(context.Background())
	if err != nil || !strings.HasPrefix(cfg.HuntMail.URL.Reveal(), "https://huntmail.hunter.workers.dev/") || len(cfg.HuntMail.Key.Reveal()) != 64 {
		t.Fatalf("saved: %v %v", cfg.HuntMail.URL.Reveal() != "", err)
	}
	if strings.Contains(page, cfg.HuntMail.Key.Reveal()) || strings.Contains(page, strings.TrimPrefix(cfg.HuntMail.URL.Reveal(), "https://huntmail.hunter.workers.dev/")) {
		t.Error("the page shows the new key or private address")
	}
}

// A refused token stops the run, and says so without the token.
func TestMailSetupRefused(t *testing.T) {
	e, saved := setupEnv(t, true)
	c := e.client()
	e.do(c, "POST", "/email/setup", url.Values{"token": {setupToken}, "domains": {"example.com"}}, nil)
	page := waitSetup(t, e)
	if !strings.Contains(page, "<h2>Stopped</h2>") || !strings.Contains(page, "the token was refused") || strings.Contains(page, setupToken) {
		t.Errorf("the stopped run:\n%s", page)
	}
	if cfg, _ := saved.Config(context.Background()); cfg.HuntMail.Enabled() {
		t.Error("saved something")
	}
}

// Without a bundle, the Setup tab has HuntMail's address and key only.
func TestMailSetupNoBundle(t *testing.T) {
	src := config.Source{LookupEnv: func(string) (string, bool) { return "", false }, ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist }}
	e := newTestEnv(t, func(d *Deps) { d.Accounts = overrides.New(d.Store, t.TempDir(), src) })
	res, body := e.do(e.client(), "GET", "/email/setup", nil, nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `id="acct-huntmail"`) || strings.Contains(body, `name="token"`) || !strings.Contains(body, "HuntMail's own setup") {
		t.Errorf("%d:\n%s", res.StatusCode, body)
	}
}

// Without anywhere to save to, or in a read-only copy, there's no setup.
func TestMailSetupAbsent(t *testing.T) {
	for name, e := range map[string]*testEnv{
		"no bundle": newTestEnv(t),
		"read-only": newTestEnv(t, func(d *Deps) { b, _ := setup.Embedded(); d.ReadOnly, d.MailBundle = true, b }),
	} {
		if res, _ := e.do(e.client(), "GET", "/email/setup", nil, nil); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", name, res.StatusCode)
		}
	}
}
