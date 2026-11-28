package weblogin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/browser"
	"github.com/mdpsec/huntdeck/internal/totp"
)

// loginSite is a sign-in in three pages: the email, then the password with
// "Remember me", then a TOTP code, after which it sets a session cookie.
func loginSite(t *testing.T, password, secret string) *httptest.Server {
	t.Helper()
	page := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!doctype html><title>Sign in</title><body>%s</body>`, body)
	}
	form := func(action, field, extra string) string {
		return `<form method="post" action="` + action + `"><input id="` + field + `" name="` + field + `" autofocus>` + extra + `</form>`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/login":
			page(w, form("/password", "email", ""))
		case "/password":
			if r.Form.Get("email") != "" {
				page(w, form("/2fa", "password", `<input type="checkbox" id="remember" name="remember">`))
				return
			}
			http.Error(w, "no email", http.StatusBadRequest)
		case "/2fa":
			if r.Form.Get("password") == "netfail" {
				page(w, `<p class="err">Failed to fetch</p>`) // the page's own request failed
				return
			}
			if r.Form.Get("password") != password {
				page(w, `<p class="err">Invalid email or password</p>`)
				return
			}
			if r.Form.Get("remember") != "on" {
				page(w, `<p class="err">Remember me wasn't ticked</p>`)
				return
			}
			page(w, form("/done", "code", ""))
		case "/done":
			// As real sites do, the code of the step before still counts:
			// typing it may cross into the next.
			now, before := time.Now(), time.Now().Add(-totp.Period)
			if cur, _ := totp.Code(secret, now); r.Form.Get("code") != cur {
				if prev, _ := totp.Code(secret, before); r.Form.Get("code") != prev {
					page(w, `<p class="err">Wrong code</p>`)
					return
				}
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "s1", Path: "/", HttpOnly: true})
			http.Redirect(w, r, "/home", http.StatusSeeOther)
		case "/home":
			page(w, `<h1>Welcome</h1>`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testFlow(srv *httptest.Server, account *Account) Flow {
	site, _ := url.Parse(srv.URL)
	return Flow{
		Who:   "test: login",
		Start: srv.URL + "/login",
		Step: `(() => {
			if (location.protocol === 'chrome-error:') return 'neterror';
			if (location.pathname === '/home') return 'in';
			const err = document.querySelector('.err');
			if (err) return 'error:' + err.innerText;
			for (const f of ['code', 'password', 'email']) if (document.getElementById(f)) return f;
			return '';
		})()`,
		StepWait: 10 * time.Second,
		Timeout:  time.Minute,
		Sites:    []*url.URL{site},
		Acts: map[string]Act{
			"email": {Sends: "the email", Repeatable: true, Do: func(ctx context.Context, p Page) error {
				if err := p.Fill(ctx, "#email", account.Email); err != nil {
					return err
				}
				return p.Press(ctx)
			}},
			"password": {Sends: "the password", Do: func(ctx context.Context, p Page) error {
				p.Tick(ctx, "#remember")
				if err := p.Fill(ctx, "#password", account.Password); err != nil {
					return err
				}
				return p.Press(ctx)
			}},
			"code": {Sends: "the 2FA code", Do: func(ctx context.Context, p Page) error {
				code, err := account.Code(ctx)
				if err != nil {
					return err
				}
				if err := p.Fill(ctx, "#code", code); err != nil {
					return err
				}
				return p.Press(ctx)
			}},
		},
	}
}

// TestFlow signs in with a real Chrome, headless; it's skipped where
// there's none.
func TestFlow(t *testing.T) {
	if _, err := browser.Find(""); err != nil {
		t.Skip("no Chrome:", err)
	}
	const secret = "JBSWY3DPEHPK3PXP"
	tests := []struct {
		name, password string
		wantErr        string // "" signs in
		rejected       bool
	}{
		{"signs in", "hunter2", "", false},
		{"a refused password is a rejection", "wrong", "Invalid email or password (after the password)", true},
		{"a failed request isn't a rejection", "netfail", "Failed to fetch (after the password)", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := loginSite(t, "hunter2", secret)
			account := &Account{Who: "test: login", Email: "hunter@example.com", Password: tt.password, TOTPSecret: secret, TOTPEnv: "TEST_TOTP"}
			jar, _ := cookiejar.New(nil)
			host := NewHost(Chrome{Headless: true})
			defer func() { _ = host.Close() }()
			err := testFlow(srv, account).Run(context.Background(), host, "test", jar)
			site, _ := url.Parse(srv.URL)
			got := ""
			for _, ck := range jar.Cookies(site) {
				if ck.Name == "session" {
					got = ck.Value
				}
			}
			switch {
			case tt.wantErr == "" && (err != nil || got != "s1"):
				t.Errorf("err %v, session %q; want signed in with s1", err, got)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr) || IsRejection(err) != tt.rejected || got != ""):
				t.Errorf("err %v (rejection %v), session %q; want %q, rejection %v", err, IsRejection(err), got, tt.wantErr, tt.rejected)
			}
		})
	}
}

// TestHostSharesOneBrowser: logins and Show use one Chrome, a tab each.
func TestHostSharesOneBrowser(t *testing.T) {
	if _, err := browser.Find(""); err != nil {
		t.Skip("no Chrome:", err)
	}
	const secret = "JBSWY3DPEHPK3PXP"
	srv := loginSite(t, "hunter2", secret)
	ctx := context.Background()
	host := NewHost(Chrome{Headless: true})
	defer func() { _ = host.Close() }()
	if host.Running() {
		t.Fatal("running before first use")
	}
	jar, _ := cookiejar.New(nil)
	account := &Account{Who: "test: login", Email: "hunter@example.com", Password: "hunter2", TOTPSecret: secret}
	if err := testFlow(srv, account).Run(ctx, host, "one", jar); err != nil {
		t.Fatal(err)
	}
	host.Register("one", srv.URL+"/login", nil)
	host.Register("two", srv.URL+"/home", fakeSession{"site.example": {{Name: "__Host-session", Value: "kept", Path: "/", Secure: true, HttpOnly: true}}})
	if got := host.Tabs(); strings.Join(got, ",") != "one,two" {
		t.Errorf("tabs %v", got)
	}
	if err := host.Show(ctx, "two"); err != nil {
		t.Fatal(err)
	}
	// Showing "one" again keeps its page: it isn't new.
	if err := host.Show(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	p1, fresh1, _ := host.Tab(ctx, "one")
	p2, fresh2, _ := host.Tab(ctx, "two")
	tabs, err := host.b.Tabs(ctx)
	if err != nil || len(tabs) != 2 || fresh1 || fresh2 || p1.ID() == p2.ID() {
		t.Errorf("tabs %+v (%v), fresh %v %v; want two tabs, one each, in one browser", tabs, err, fresh1, fresh2)
	}
	if u := p1.URL(ctx); !strings.HasSuffix(u, "/home") {
		t.Errorf("tab one is at %s, want still signed in at /home", u)
	}
	// "two" shows the session HuntDeck keeps: the browser has it now.
	cookies, _ := host.cookies(ctx)
	if !slices.ContainsFunc(cookies, func(c *http.Cookie) bool {
		return c.Name == "__Host-session" && c.Value == "kept" && c.Domain == "site.example" && c.Secure && c.HttpOnly
	}) {
		t.Errorf("the browser's cookies lack the kept session: %d cookies", len(cookies))
	}
	if err := host.Close(); err != nil || host.Running() {
		t.Errorf("close: %v, running %v", err, host.Running())
	}
	// The next use starts it again.
	if err := host.Show(ctx, "two"); err != nil || !host.Running() {
		t.Errorf("after closing, show: %v, running %v", err, host.Running())
	}
}

// fakeSession is a kept session: cookies by the host that set them.
type fakeSession map[string][]*http.Cookie

func (f fakeSession) Hosts() []string {
	var out []string
	for h := range f {
		out = append(out, h)
	}
	return out
}

func (f fakeSession) Saved(host string) []*http.Cookie { return f[host] }

// Remember ticks the boxes that ask to remember the browser, whether the
// box or only its label shows, and leaves the others alone.
func TestRemember(t *testing.T) {
	if _, err := browser.Find(""); err != nil {
		t.Skip("no Chrome:", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><body><form>
			<label><input type="checkbox" id="a"> Remember me</label>
			<input type="checkbox" id="b" name="rememberDevice" style="display:none"><label for="b">Don't ask again on this device</label>
			<label><input type="checkbox" id="c"> Keep me signed in</label>
			<label><input type="checkbox" id="d"> Email me the newsletter</label>
			<label><input type="checkbox" id="e"> I accept the terms</label>
		</form></body>`)
	}))
	defer srv.Close()
	ctx := context.Background()
	host := NewHost(Chrome{Headless: true})
	defer func() { _ = host.Close() }()
	bp, _, err := host.Tab(ctx, "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := bp.Navigate(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	p := Page{bp}
	if _, err := p.WaitFor(ctx, "document.getElementById('e') && 'ok'", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	p.Remember(ctx)
	var got string
	if err := p.Eval(ctx, `['a','b','c','d','e'].map(id => document.getElementById(id).checked ? 1 : 0).join('')`, &got); err != nil {
		t.Fatal(err)
	}
	if got != "11100" {
		t.Errorf("ticked %s, want 11100 (remember, trust, keep signed in; not the newsletter or terms)", got)
	}
}

// Take reads the signed-in page, for a session held outside cookies; a
// tab whose session is in localStorage gets it there when shown.
func TestTakeAndStorageSession(t *testing.T) {
	if _, err := browser.Find(""); err != nil {
		t.Skip("no Chrome:", err)
	}
	const secret = "JBSWY3DPEHPK3PXP"
	srv := loginSite(t, "hunter2", secret)
	ctx := context.Background()
	host := NewHost(Chrome{Headless: true})
	defer func() { _ = host.Close() }()
	f := testFlow(srv, &Account{Who: "test: login", Email: "hunter@example.com", Password: "hunter2", TOTPSecret: secret})
	var at string
	f.Take = func(ctx context.Context, p Page) error { return p.Eval(ctx, "location.pathname", &at) }
	if err := f.Run(ctx, host, "one", nil); err != nil || at != "/home" {
		t.Fatalf("Run with Take: %v, took %q", err, at)
	}

	host.Register("store", srv.URL+"/home", storageSession{srv.URL, map[string]string{"access_token": "tok-1"}})
	if err := host.Show(ctx, "store"); err != nil {
		t.Fatal(err)
	}
	p, _, _ := host.Tab(ctx, "store")
	var tok string
	if err := p.Eval(ctx, "localStorage.getItem('access_token')", &tok); err != nil || tok != "tok-1" {
		t.Errorf("the tab's storage holds %q (%v), want the kept token", tok, err)
	}
	if u := p.URL(ctx); !strings.HasSuffix(u, "/home") {
		t.Errorf("tab at %s, want its home", u)
	}
}

type storageSession struct {
	origin string
	items  map[string]string
}

func (storageSession) Hosts() []string                             { return nil }
func (storageSession) Saved(string) []*http.Cookie                 { return nil }
func (s storageSession) LocalStorage() (string, map[string]string) { return s.origin, s.items }

func TestTransient(t *testing.T) {
	for msg, want := range map[string]bool{
		"Failed to fetch": true, "NetworkError when attempting to fetch resource.": true, "Request timed out": true,
		"Please try again later": true, "Invalid email or password": false, "Wrong code": false, "Your account is locked": false,
	} {
		if transient(msg) != want {
			t.Errorf("transient(%q) = %v, want %v", msg, !want, want)
		}
	}
}

func TestFlowNextStepWaitBoundsRepeatedStep(t *testing.T) {
	if _, err := browser.Find(""); err != nil {
		t.Skip("no Chrome:", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><form><input id="u"><input type="password" id="p"></form>`)
	}))
	defer srv.Close()

	ctx := context.Background()
	host := NewHost(Chrome{Headless: true})
	defer func() { _ = host.Close() }()

	f := Flow{
		Who:      "test: login",
		Start:    srv.URL,
		Step:     "document.getElementById('p') ? 'login' : ''",
		StepWait: 1200 * time.Millisecond,
		Timeout:  10 * time.Second,
		Pause: func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(min(d, 100*time.Millisecond))
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		},
		Acts: map[string]Act{
			"login": {Do: func(context.Context, Page) error { return nil }}, // page never transitions away from "login"
		},
	}
	start := time.Now()
	err := f.Run(ctx, host, "stuck", nil)
	took := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "didn't move on from login") || !strings.Contains(err.Error(), `still at "login"`) {
		t.Fatalf("err = %v, want bounded step error naming login", err)
	}
	if took > 5*time.Second {
		t.Fatalf("took %s, want StepWait (~1.2s) instead of full Timeout (10s)", took)
	}
}

func TestHostClearCookies(t *testing.T) {
	if _, err := browser.Find(""); err != nil {
		t.Skip("no Chrome:", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><body>ok</body>`)
	}))
	defer srv.Close()

	ctx := context.Background()
	host := NewHost(Chrome{Headless: true})
	defer func() { _ = host.Close() }()
	bp, _, err := host.Tab(ctx, "cookies")
	if err != nil {
		t.Fatal(err)
	}
	if err := bp.Navigate(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	b := host.b
	if err := b.SetCookies(ctx, "user.vulbox.com", []*http.Cookie{
		{Name: "vulbox_token", Value: "host-tok", Path: "/", Secure: true},
		{Name: "vulbox_token", Value: "dom-tok", Domain: "vulbox.com", Path: "/", Secure: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.SetCookies(ctx, "other.example", []*http.Cookie{
		{Name: "vulbox_token", Value: "keep-me", Path: "/", Secure: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := host.ClearCookies(ctx, "vulbox.com", []string{"vulbox_token"}); err != nil {
		t.Fatal(err)
	}
	cookies, err := host.cookies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var keptOther bool
	for _, ck := range cookies {
		if strings.HasSuffix(strings.TrimPrefix(ck.Domain, "."), "vulbox.com") && ck.Name == "vulbox_token" {
			t.Errorf("stale vulbox_token cookie survived ClearCookies: %+v", ck)
		}
		if ck.Domain == "other.example" && ck.Value == "keep-me" {
			keptOther = true
		}
	}
	if !keptOther {
		t.Error("ClearCookies removed unrelated other.example cookie")
	}
}
