package inspectiv

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/sessionjar"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func newTestClient(t *testing.T, opts Options) *Client {
	t.Helper()
	if opts.Rate == 0 {
		opts.Rate = 100
	}
	c, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.http = httpclient.New(httpclient.Options{
		RatePerSecond: opts.Rate,
		Burst:         10,
		Jar:           c.jar,
		RedirectHosts: []string{c.base.Host, c.web.Host},
		Sleep:         func(context.Context, time.Duration) error { return nil },
	})
	return c
}

func TestCredentialsAndTokenNormalizationAndPersistence(t *testing.T) {
	rawHex := "0123456789abcdef0123456789abcdef01234567"

	t.Run("email and password must be paired", func(t *testing.T) {
		if _, err := New(Options{Email: "user@example.com"}); err == nil {
			t.Fatal("expected error when Email is set without Password")
		}
		if _, err := New(Options{Password: "sample-pass"}); err == nil {
			t.Fatal("expected error when Password is set without Email")
		}
	})

	t.Run("raw and prefixed DRF tokens normalize and persist in sessionjar", func(t *testing.T) {
		dir := t.TempDir()
		jarPath := filepath.Join(dir, "inspectiv.json")
		jar, err := sessionjar.Open(jarPath, "api.bounty.inspectiv.com", "app.inspectiv.com")
		if err != nil {
			t.Fatal(err)
		}

		c := newTestClient(t, Options{
			Token: "Token " + rawHex,
			Jar:   jar,
		})
		if got := c.currentToken(); got != rawHex {
			t.Fatalf("currentToken() = %q, want %q", got, rawHex)
		}
		creds := c.Credentials()
		if len(creds) != 1 || creds[0].Name != "API token" {
			t.Fatalf("Credentials() = %+v, want [API token]", creds)
		}

		// Reopen sessionjar from disk and verify synthetic cookie restores token.
		reopenedJar, err := sessionjar.Open(jarPath, "api.bounty.inspectiv.com", "app.inspectiv.com")
		if err != nil {
			t.Fatal(err)
		}
		c2 := newTestClient(t, Options{Jar: reopenedJar})
		if got := c2.currentToken(); got != rawHex {
			t.Fatalf("reopened currentToken() = %q, want %q", got, rawHex)
		}
	})

	t.Run("website login credential name and BrowserSession localStorage", func(t *testing.T) {
		c := newTestClient(t, Options{
			Email:    "SampleUser@Example.COM",
			Password: "sample-password",
			Token:    "token " + rawHex,
			WebHost:  weblogin.NewHost(weblogin.Chrome{}),
		})
		creds := c.Credentials()
		if len(creds) != 1 || creds[0].Name != "Website login" {
			t.Fatalf("Credentials() = %+v, want [Website login]", creds)
		}
		if got := c.Account(); got != "sampleuser@example.com" {
			t.Fatalf("Account() = %q, want %q", got, "sampleuser@example.com")
		}
		ss, ok := c.BrowserSession().(weblogin.StorageSession)
		if !ok {
			t.Fatal("BrowserSession should implement weblogin.StorageSession")
		}
		origin, items := ss.LocalStorage()
		if origin != "https://app.inspectiv.com" || items[localStorageKey] != rawHex {
			t.Fatalf("LocalStorage() = (%q, %v), want origin https://app.inspectiv.com and token", origin, items)
		}
	})
}

func TestKeepAliveAndAccountNamer(t *testing.T) {
	rawHex := "0123456789abcdef0123456789abcdef01234567"
	var logoutCalls atomic.Int32
	var userCalls, profileCalls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token "+rawHex {
			t.Errorf("Authorization = %q, want Token %s", r.Header.Get("Authorization"), rawHex)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/accounts/user":
			userCalls.Add(1)
			_, _ = w.Write(readFixture(t, "user.json"))
		case "/accounts/profile/":
			profileCalls.Add(1)
			_, _ = w.Write(readFixture(t, "profile.json"))
		case "/accounts/logout/":
			logoutCalls.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, Options{BaseURL: srv.URL, Token: rawHex})
	if err := c.KeepAlive(context.Background()); err != nil {
		t.Fatalf("KeepAlive: %v", err)
	}
	if got := c.Account(); got != "sample_user" {
		t.Fatalf("Account() = %q, want sample_user", got)
	}
	if userCalls.Load() != 1 || profileCalls.Load() != 1 {
		t.Fatalf("calls user=%d profile=%d, want 1/1", userCalls.Load(), profileCalls.Load())
	}
	if logoutCalls.Load() != 0 {
		t.Fatalf("logout must never be called, got %d", logoutCalls.Load())
	}
	creds := c.Credentials()
	if len(creds) != 1 || creds[0].LastOK == nil || creds[0].Failure != "" {
		t.Fatalf("expected accepted credential, got %+v", creds)
	}
}

func TestHTTPErrors401429And5xx(t *testing.T) {
	t.Run("401 marks pasted token stuck and returns ErrSessionExpired", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Invalid token.","status_code":401}`))
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, Token: "0123456789abcdef0123456789abcdef01234567"})
		err := c.KeepAlive(context.Background())
		if !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("expected ErrSessionExpired, got %v", err)
		}
		creds := c.Credentials()
		if len(creds) != 1 || !creds[0].Stuck || creds[0].Failure == "" {
			t.Fatalf("expected stuck refused credential, got %+v", creds)
		}
	})

	t.Run("429 retries politely and records rate limit", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) <= 2 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"Request was throttled.","status_code":429}`))
				return
			}
			_, _ = w.Write(readFixture(t, "user.json"))
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, Token: "0123456789abcdef0123456789abcdef01234567"})
		var out accountUserResp
		if err := c.getJSONNoRenew(context.Background(), "/accounts/user", &out); err != nil {
			t.Fatalf("expected retry success, got %v", err)
		}
		if calls.Load() != 3 {
			t.Fatalf("calls = %d, want 3", calls.Load())
		}
		_, limited := c.Stats()
		if limited != 2 {
			t.Fatalf("rateLimited = %d, want 2", limited)
		}
	})

	t.Run("5xx returns HTTP error after retries and 400 returns apiError", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/bad" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"bad request","status_code":400}`))
				return
			}
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"upstream gateway error","status_code":502}`))
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, Token: "0123456789abcdef0123456789abcdef01234567"})
		var out accountUserResp
		err := c.getJSONNoRenew(context.Background(), "/accounts/user", &out)
		if err == nil || !strings.Contains(err.Error(), "HTTP 502") {
			t.Fatalf("expected HTTP 502 error, got %v", err)
		}
		err = c.getJSONNoRenew(context.Background(), "/bad", &out)
		var ae *apiError
		if !errors.As(err, &ae) || ae.StatusCode != http.StatusBadRequest || ae.Message != "bad request" {
			t.Fatalf("expected apiError 400 bad request, got %v", err)
		}
	})
}

func TestBrowserSignInCheckpointRollbackAndRecaptchaSafety(t *testing.T) {
	dir := t.TempDir()
	jarPath := filepath.Join(dir, "inspectiv.json")
	loginState := filepath.Join(dir, "inspectiv-login.json")

	oldToken := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	newToken := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	var activeToken atomic.Value
	activeToken.Store(oldToken)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "Token " + activeToken.Load().(string)
		if r.Header.Get("Authorization") != want {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Invalid token.","status_code":401}`))
			return
		}
		switch r.URL.Path {
		case "/accounts/user":
			_, _ = w.Write(readFixture(t, "user.json"))
		case "/accounts/profile/":
			_, _ = w.Write(readFixture(t, "profile.json"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	jar, err := sessionjar.Open(jarPath, "api.bounty.inspectiv.com", "app.inspectiv.com")
	if err != nil {
		t.Fatal(err)
	}

	c := newTestClient(t, Options{
		BaseURL:    srv.URL,
		Email:      "sample@example.com",
		Password:   "sample-pw",
		Jar:        jar,
		LoginState: loginState,
		WebHost:    weblogin.NewHost(weblogin.Chrome{}),
	})
	c.seedToken(oldToken)

	// Simulate failed browser sign-in due to interactive reCAPTCHA challenge
	// that clears token mid-attempt: Checkpoint/Rollback must restore oldToken
	// and the failure must be non-sticky (Stuck == false).
	c.browserLoginFn = func(ctx context.Context) error {
		c.seedToken("")
		return errors.New("inspectiv: website login: Google reCAPTCHA challenge appeared; open Settings > Browser (VNC) to complete sign-in, or paste INS_TOKEN")
	}

	err = c.browserSignIn(context.Background())
	if err == nil || !strings.Contains(err.Error(), "reCAPTCHA") {
		t.Fatalf("expected reCAPTCHA error, got %v", err)
	}
	if weblogin.IsRejection(err) {
		t.Fatal("reCAPTCHA failure must be non-sticky (not a weblogin.Rejection)")
	}
	if got := c.currentToken(); got != oldToken {
		t.Fatalf("rollback failed in memory: got %q, want %q", got, oldToken)
	}
	reopened, err := sessionjar.Open(jarPath, "api.bounty.inspectiv.com", "app.inspectiv.com")
	if err != nil {
		t.Fatal(err)
	}
	if got := newTestClient(t, Options{Jar: reopened}).currentToken(); got != oldToken {
		t.Fatalf("rollback failed on disk: got %q, want %q", got, oldToken)
	}
	if creds := c.Credentials(); len(creds) != 1 || creds[0].Stuck {
		t.Fatalf("expected non-stuck credential after reCAPTCHA, got %+v", creds)
	}

	// Reset login history wait window and simulate 401 -> automatic browser renewal.
	c.logins = weblogin.NewHistory(filepath.Join(dir, "inspectiv-login-2.json"))
	activeToken.Store(newToken)
	c.browserLoginFn = func(ctx context.Context) error {
		c.seedToken(newToken)
		return nil
	}
	var u accountUserResp
	if err := c.getJSON(context.Background(), "/accounts/user", &u); err != nil {
		t.Fatalf("expected renewal on 401 to succeed, got %v", err)
	}
	if got := c.currentToken(); got != newToken {
		t.Fatalf("expected new token %q, got %q", newToken, got)
	}

	// Sanity check loginStepJS contains required selectors and reCAPTCHA guard.
	for _, needle := range []string{
		"recaptcha/api2/bframe",
		"localStorage.getItem('inspectiv')",
		"form.c-signup-form #email",
		"form.c-signup-form #password",
	} {
		if !strings.Contains(loginStepJS, needle) {
			t.Errorf("loginStepJS missing %q", needle)
		}
	}
}

func TestListProgramsAndMetaCacheAndMarkdownParsing(t *testing.T) {
	var allCalls, detailCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/accounts/user":
			_, _ = w.Write(readFixture(t, "user.json"))
		case "/accounts/profile/":
			_, _ = w.Write(readFixture(t, "profile.json"))
		case "/programs/all/":
			allCalls.Add(1)
			_, _ = w.Write(readFixture(t, "programs_all.json"))
		case "/programs/p4D5e6F7g8H9i0J1k2L3m4/":
			detailCalls.Add(1)
			_, _ = w.Write(readFixture(t, "program_detail.json"))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, Options{
		BaseURL: srv.URL,
		Token:   "0123456789abcdef0123456789abcdef01234567",
	})

	listing, err := c.ListPrograms(context.Background())
	if err != nil {
		t.Fatalf("ListPrograms: %v", err)
	}
	if !listing.Complete || len(listing.Programs) != 3 {
		t.Fatalf("listing complete=%v len=%d, want true/3", listing.Complete, len(listing.Programs))
	}
	if allCalls.Load() != 1 {
		t.Fatalf("expected 1 /programs/all/ call, got %d", allCalls.Load())
	}

	byHandle := map[string]platform.Program{}
	for _, p := range listing.Programs {
		byHandle[p.Handle] = p
	}

	acme := byHandle["p1A2b3C4d5E6f7G8h9I0j1"]
	if acme.State != platform.StateOpen || acme.Private || acme.Currency != "USD" || !acme.OffersBounty {
		t.Fatalf("unexpected acme program: %+v", acme)
	}
	if acme.URL != "https://app.inspectiv.com/#/programs/p1A2b3C4d5E6f7G8h9I0j1" {
		t.Errorf("acme URL = %q", acme.URL)
	}
	if acme.BountyMin == nil || *acme.BountyMin != 50 || acme.BountyMax == nil || *acme.BountyMax != 2500 {
		t.Errorf("acme bounty bounds = %v..%v, want 50..2500", acme.BountyMin, acme.BountyMax)
	}
	if !strings.Contains(acme.Policy, "# Severity") {
		t.Errorf("expected Policy to hold details Markdown")
	}

	globex := byHandle["p2B3c4D5e6F7g8H9i0J1k2"]
	if globex.State != platform.StatePaused || !globex.Private || globex.OffersBounty {
		t.Errorf("unexpected globex program: %+v", globex)
	}

	initech := byHandle["p3C4d5E6f7G8h9I0j1K2l3"]
	if initech.State != platform.StateClosed || !initech.OffersBounty {
		t.Errorf("unexpected initech program: %+v", initech)
	}

	// FetchScopeDetail for cached acme must NOT make any HTTP call.
	entries, detail, err := c.FetchScopeDetail(context.Background(), acme)
	if err != nil {
		t.Fatalf("FetchScopeDetail(acme): %v", err)
	}
	if detailCalls.Load() != 0 {
		t.Fatalf("expected 0 detail HTTP calls when cached, got %d", detailCalls.Load())
	}

	if detail.Testing == nil || detail.Testing.Header != "X-Inspectiv-Tester: {researcher-username}" {
		t.Errorf("TestingRules = %+v, want X-Inspectiv-Tester header", detail.Testing)
	}
	if len(detail.Rewards) != 3 {
		t.Fatalf("len(Rewards) = %d, want 3 tiers", len(detail.Rewards))
	}
	if detail.Rewards[2].Scope != "Tier 3" || detail.Rewards[2].Critical.Max != 2500 || detail.Rewards[2].Low.Max != 200 {
		t.Errorf("Tier 3 reward row = %+v", detail.Rewards[2])
	}

	// Check scope entries: 3 domains + 1 URL + 1 Android (in-scope), 2 out-of-scope.
	var inScope, outScope []platform.ScopeEntry
	byIdent := map[string]platform.ScopeEntry{}
	for _, e := range entries {
		byIdent[e.Identifier] = e
		if e.InScope {
			inScope = append(inScope, e)
		} else {
			outScope = append(outScope, e)
		}
	}
	if len(inScope) != 5 {
		t.Fatalf("len(inScope) = %d, want 5: %+v", len(inScope), inScope)
	}
	if len(outScope) != 2 {
		t.Fatalf("len(outScope) = %d, want 2: %+v", len(outScope), outScope)
	}
	if got := byIdent["app.example.com"].Instruction; !strings.Contains(got, "Tier 2") || !strings.Contains(got, "Credentials: Public signup") {
		t.Errorf("app.example.com Instruction = %q", got)
	}
	if got := byIdent["https://api.example.com/v2"]; got.AssetType != scope.AssetURL || !got.InScope {
		t.Errorf("api URL entry = %+v", got)
	}
	if got := byIdent["https://play.google.com/store/apps/details?id=com.example.acme"]; got.AssetType != scope.AssetAndroid || !got.InScope {
		t.Errorf("android entry = %+v", got)
	}
	if got := byIdent["*.staging.example.com"]; got.InScope || got.AssetType != scope.AssetWildcard {
		t.Errorf("out-of-scope wildcard = %+v", got)
	}
	if got := byIdent["https://status.example.com/admin"]; got.InScope || got.AssetType != scope.AssetURL {
		t.Errorf("out-of-scope URL = %+v", got)
	}

	// FetchScope returns identical entries.
	entriesOnly, err := c.FetchScope(context.Background(), acme)
	if err != nil || len(entriesOnly) != len(entries) {
		t.Fatalf("FetchScope len=%d err=%v, want %d", len(entriesOnly), err, len(entries))
	}

	// FetchScopeDetail for uncached UUID falls back to GET /programs/{uuid}/.
	uncached := platform.Program{Handle: "p4D5e6F7g8H9i0J1k2L3m4"}
	uEntries, uDetail, err := c.FetchScopeDetail(context.Background(), uncached)
	if err != nil {
		t.Fatalf("FetchScopeDetail(uncached): %v", err)
	}
	if detailCalls.Load() != 1 {
		t.Fatalf("expected 1 fallback detail call, got %d", detailCalls.Load())
	}
	if len(uEntries) != 1 || uEntries[0].Identifier != "standalone.example.com" {
		t.Errorf("uncached entries = %+v", uEntries)
	}
	if uDetail.Testing == nil || uDetail.Testing.Header != "X-Inspectiv-Tester: sample_user" {
		t.Errorf("uncached Testing = %+v", uDetail.Testing)
	}
	if len(uDetail.Rewards) != 1 || uDetail.Rewards[0].Critical.Min != 2000 || uDetail.Rewards[0].Critical.Max != 4000 {
		t.Errorf("uncached Rewards = %+v", uDetail.Rewards)
	}
}

func TestFetchReportsPaginationAndMapping(t *testing.T) {
	var detailOrThreadCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/threads/") || (strings.HasPrefix(r.URL.Path, "/submissions/") && r.URL.Path != "/submissions/list/") {
			detailOrThreadCalls.Add(1)
		}
		if r.URL.Query().Get("start_date") != "" || r.URL.Query().Get("end_date") != "" {
			t.Errorf("start_date/end_date must be omitted, got %s", r.URL.RawQuery)
		}
		switch r.URL.Path {
		case "/accounts/user":
			_, _ = w.Write(readFixture(t, "user.json"))
		case "/accounts/profile/":
			_, _ = w.Write(readFixture(t, "profile.json"))
		case "/submissions/list/":
			if r.URL.Query().Get("page") == "2" {
				_, _ = w.Write(readFixture(t, "submissions_p2.json"))
			} else {
				_, _ = w.Write(readFixture(t, "submissions_p1.json"))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	unauth := newTestClient(t, Options{BaseURL: srv.URL})
	if unauth.ReportsConfigured() {
		t.Fatal("expected ReportsConfigured=false without credentials")
	}

	c := newTestClient(t, Options{BaseURL: srv.URL, Token: "0123456789abcdef0123456789abcdef01234567"})
	if !c.ReportsConfigured() {
		t.Fatal("expected ReportsConfigured=true with token")
	}

	var got []platform.Report
	complete, err := c.FetchReports(context.Background(), nil, 10, func(_ context.Context, batch []platform.Report) error {
		got = append(got, batch...)
		return nil
	})
	if err != nil {
		t.Fatalf("FetchReports: %v", err)
	}
	if !complete {
		t.Fatal("expected complete=true")
	}
	if detailOrThreadCalls.Load() != 0 {
		t.Fatalf("must NEVER call submission/thread detail endpoints, got %d", detailOrThreadCalls.Load())
	}
	if len(got) != 4 {
		t.Fatalf("got %d reports, want 4", len(got))
	}

	byKey := map[string]platform.Report{}
	for _, r := range got {
		byKey[r.Key] = r
	}

	r1 := byKey["s1A2b3C4d5E6f7G8h9I0j1"]
	if r1.State != platform.ReportResolved || r1.Severity != platform.SeverityHigh || r1.ProgramHandle != "p1A2b3C4d5E6f7G8h9I0j1" {
		t.Errorf("r1 = %+v", r1)
	}
	if !r1.PaymentsKnown || len(r1.Payments) != 1 || r1.Payments[0].Amount != 500 || r1.Payments[0].Currency != "USD" {
		t.Errorf("r1 payments = %+v", r1.Payments)
	}

	r2 := byKey["s2B3c4D5e6F7g8H9i0J1k2"]
	if r2.State != platform.ReportTriaged || r2.WaitingOn != "program" || r2.Severity != platform.SeverityCritical {
		t.Errorf("r2 = %+v", r2)
	}

	r3 := byKey["s3C4d5E6f7G8h9I0j1K2l3"]
	if r3.State != platform.ReportDuplicate || r3.Severity != platform.SeverityLow {
		t.Errorf("r3 = %+v", r3)
	}

	r4 := byKey["s4D5e6F7g8H9i0J1k2L3m4"]
	if r4.State != platform.ReportNotApplicable || r4.Severity != platform.SeverityNone {
		t.Errorf("r4 = %+v", r4)
	}

	// Partial page 2 failure returns complete=false alongside error.
	srvFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/accounts/user":
			_, _ = w.Write(readFixture(t, "user.json"))
		case "/accounts/profile/":
			_, _ = w.Write(readFixture(t, "profile.json"))
		case "/submissions/list/":
			if r.URL.Query().Get("page") == "2" {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"db error","status_code":500}`))
				return
			}
			_, _ = w.Write(readFixture(t, "submissions_p1.json"))
		}
	}))
	defer srvFail.Close()

	cFail := newTestClient(t, Options{BaseURL: srvFail.URL, Token: "0123456789abcdef0123456789abcdef01234567"})
	cFail.http = httpclient.New(httpclient.Options{RatePerSecond: 100, MaxAttempts: 1})
	var partial []platform.Report
	completeFail, err := cFail.FetchReports(context.Background(), nil, 10, func(_ context.Context, batch []platform.Report) error {
		partial = append(partial, batch...)
		return nil
	})
	if err == nil || completeFail || len(partial) != 2 {
		t.Fatalf("expected error, complete=false, 2 partial reports; got err=%v complete=%v len=%d", err, completeFail, len(partial))
	}
}

func TestFetchProgramStats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/accounts/user":
			_, _ = w.Write(readFixture(t, "user.json"))
		case "/accounts/profile/":
			_, _ = w.Write(readFixture(t, "profile.json"))
		case "/programs/all/":
			_, _ = w.Write(readFixture(t, "programs_all.json"))
		case "/programs/p4D5e6F7g8H9i0J1k2L3m4/":
			_, _ = w.Write(readFixture(t, "program_detail.json"))
		case "/programs/missing-program/":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Not found.","status_code":404}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, Options{BaseURL: srv.URL, Token: "0123456789abcdef0123456789abcdef01234567"})
	if _, err := c.ListPrograms(context.Background()); err != nil {
		t.Fatalf("ListPrograms: %v", err)
	}

	statsByHandle := map[string]platform.ProgramStats{}
	var covered []string
	handles := []string{
		"p1A2b3C4d5E6f7G8h9I0j1",
		"p2B3c4D5e6F7g8H9i0J1k2",
		"p4D5e6F7g8H9i0J1k2L3m4",
		"missing-program",
	}
	err := c.FetchProgramStats(context.Background(), handles, func(b []string, s map[string]platform.ProgramStats) error {
		covered = append(covered, b...)
		for k, v := range s {
			statsByHandle[k] = v
		}
		return nil
	})
	if err != nil {
		t.Fatalf("FetchProgramStats: %v", err)
	}
	if len(covered) != len(handles) {
		t.Fatalf("covered = %v, want %v", covered, handles)
	}

	st1 := statsByHandle["p1A2b3C4d5E6f7G8h9I0j1"]
	if st1.Currency != "USD" || st1.HackersThanked == nil || *st1.HackersThanked != 2 || len(st1.Rewards) != 3 {
		t.Errorf("st1 = %+v", st1)
	}

	st2 := statsByHandle["p2B3c4D5e6F7g8H9i0J1k2"]
	if st2.HackersThanked == nil || *st2.HackersThanked != 0 {
		t.Errorf("st2 = %+v", st2)
	}

	st4 := statsByHandle["p4D5e6F7g8H9i0J1k2L3m4"]
	if st4.HackersThanked == nil || *st4.HackersThanked != 1 || len(st4.Rewards) != 1 {
		t.Errorf("st4 = %+v", st4)
	}

	if _, ok := statsByHandle["missing-program"]; ok {
		t.Error("did not expect stats entry for 404 program")
	}

	// Verify Context cancellation propagates cleanly.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.FetchProgramStats(ctx, []string{"p1A2b3C4d5E6f7G8h9I0j1"}, func([]string, map[string]platform.ProgramStats) error { return nil }); err == nil {
		t.Fatal("expected context canceled error")
	}
	_ = time.Second
}
