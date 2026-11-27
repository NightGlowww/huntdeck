package gobugfree

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func makeTestJWT(t *testing.T, typ, sub string, exp int64) string {
	t.Helper()
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claims := map[string]any{"iat": 1700000000, "exp": exp}
	if typ != "" {
		claims["typ"] = typ
	}
	if sub != "" {
		claims["preferred_username"] = sub
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	pl := base64.RawURLEncoding.EncodeToString(payload)
	sig := base64.RawURLEncoding.EncodeToString([]byte("synthetic-signature"))
	return hdr + "." + pl + "." + sig
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
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func TestCredentialsAndTokenFormats(t *testing.T) {
	expUnix := int64(1893456000) // 2030-01-01T00:00:00Z
	accessJWT := makeTestJWT(t, "Bearer", "sample_user", expUnix)
	refreshJWT := makeTestJWT(t, "Refresh", "sample_user", expUnix+3600)

	tests := []struct {
		name        string
		opts        Options
		wantLen     int
		wantName    string
		wantAcct    string
		wantAccess  string
		wantRefresh string
		wantCookie  bool
	}{
		{
			name:     "public only returns nil credentials",
			opts:     Options{PublicOnly: true, Token: accessJWT, Email: "sample@example.com", Password: "secret"},
			wantLen:  0,
			wantAcct: "",
		},
		{
			name:       "bearer access token",
			opts:       Options{Token: "Bearer " + accessJWT},
			wantLen:    1,
			wantName:   "API token",
			wantAcct:   "sample_user",
			wantAccess: accessJWT,
		},
		{
			name:        "keycloak refresh token JWT",
			opts:        Options{Token: refreshJWT},
			wantLen:     1,
			wantName:    "API token",
			wantAcct:    "sample_user",
			wantRefresh: refreshJWT,
		},
		{
			name:       "session cookie value",
			opts:       Options{Token: "session=synthetic-session-cookie"},
			wantLen:    1,
			wantName:   "API token",
			wantCookie: true,
		},
		{
			name:     "website login uses Website login credential name",
			opts:     Options{Email: "SampleUser@Example.COM", Password: "secret", WebHost: weblogin.NewHost(weblogin.Chrome{})},
			wantLen:  1,
			wantName: "Website login",
			wantAcct: "sampleuser@example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, tc.opts)
			if got := c.Account(); got != tc.wantAcct {
				t.Errorf("Account() = %q, want %q", got, tc.wantAcct)
			}
			creds := c.Credentials()
			if len(creds) != tc.wantLen {
				t.Fatalf("len(Credentials()) = %d, want %d", len(creds), tc.wantLen)
			}
			if tc.wantLen == 0 {
				return
			}
			if creds[0].Name != tc.wantName {
				t.Errorf("Credential.Name = %q, want %q", creds[0].Name, tc.wantName)
			}
			gotA, gotR := c.tokens()
			if gotA != tc.wantAccess || gotR != tc.wantRefresh {
				t.Errorf("tokens = (%q, %q), want (%q, %q)", gotA, gotR, tc.wantAccess, tc.wantRefresh)
			}
			if tc.wantCookie && !c.hasSessionCookie() {
				t.Error("expected session cookie in jar")
			}
		})
	}

	if _, err := New(Options{Email: "only-email@example.com"}); err == nil {
		t.Fatal("expected error when Email is set without Password")
	}
}

func TestPublicModeProgramsAndScope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("expected Accept: application/json, got %q", r.Header.Get("Accept"))
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected Authorization header in public mode")
		}
		switch r.URL.Path {
		case "/public/engagements":
			_, _ = w.Write(readFixture(t, "public_engagements.json"))
		case "/public/engagements/acme/acme-bounty":
			_, _ = w.Write(readFixture(t, "engagement_detail_acme.json"))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, Options{BaseURL: srv.URL, PublicOnly: true})
	listing, err := c.ListPrograms(context.Background())
	if err != nil {
		t.Fatalf("ListPrograms: %v", err)
	}
	if !listing.Complete || len(listing.Programs) != 2 {
		t.Fatalf("got %d programs complete=%v, want 2 complete=true", len(listing.Programs), listing.Complete)
	}
	acme := listing.Programs[0]
	if acme.Handle != "acme--acme-bounty" || acme.PlatformID != "acme/acme-bounty" {
		t.Errorf("unexpected handle/platformID: %q / %q", acme.Handle, acme.PlatformID)
	}
	if acme.URL != srv.URL+"/public/engagements/acme/acme-bounty" {
		t.Errorf("unexpected public URL: %q", acme.URL)
	}
	if !acme.OffersBounty || listing.Programs[1].OffersBounty {
		t.Errorf("unexpected OffersBounty flags: acme=%v globex=%v", acme.OffersBounty, listing.Programs[1].OffersBounty)
	}

	entries, detail, err := c.FetchScopeDetail(context.Background(), acme)
	if err != nil {
		t.Fatalf("FetchScopeDetail: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected parsed scope entries")
	}
	if detail.Requirements == nil || !detail.Requirements.IDVerified || !detail.Requirements.Terms {
		t.Errorf("unexpected Requirements: %+v", detail.Requirements)
	}
	if detail.Facts == nil || detail.Facts.SafeHarbor != "full" || detail.Facts.OffersBounty == nil || !*detail.Facts.OffersBounty {
		t.Errorf("unexpected Facts: %+v", detail.Facts)
	}
}

func TestAuthenticatedPaginationRefreshAndPartialFailure(t *testing.T) {
	expFuture := time.Now().Add(time.Hour).Unix()
	freshAccess := makeTestJWT(t, "Bearer", "sample_user", expFuture)
	refreshTok := makeTestJWT(t, "Refresh", "sample_user", expFuture+3600)

	t.Run("refresh_token exchanges at IDURL and paginates 24 per page", func(t *testing.T) {
		var tokenCalls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/protocol/openid-connect/token":
				tokenCalls.Add(1)
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("client_id") != "agent" || r.Form.Get("refresh_token") != refreshTok {
					t.Errorf("unexpected token form: %v", r.Form)
				}
				_ = json.NewEncoder(w).Encode(tokenResponse{
					AccessToken:  freshAccess,
					RefreshToken: refreshTok,
					ExpiresIn:    300,
				})
			case "/engagements":
				if r.Header.Get("Authorization") != "Bearer "+freshAccess {
					t.Errorf("missing/wrong Authorization header: %q", r.Header.Get("Authorization"))
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if r.URL.Query().Get("page") == "2" {
					_, _ = w.Write(readFixture(t, "engagements_p2.json"))
				} else {
					_, _ = w.Write(readFixture(t, "engagements_p1.json"))
				}
			default:
				t.Errorf("unexpected path: %s", r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, IDURL: srv.URL, Token: refreshTok})
		listing, err := c.ListPrograms(context.Background())
		if err != nil {
			t.Fatalf("ListPrograms: %v", err)
		}
		if tokenCalls.Load() != 1 {
			t.Errorf("expected 1 refresh call, got %d", tokenCalls.Load())
		}
		if !listing.Complete || len(listing.Programs) != 26 {
			t.Fatalf("got %d programs complete=%v, want 26 complete=true", len(listing.Programs), listing.Complete)
		}

		byHandle := map[string]platform.Program{}
		for _, p := range listing.Programs {
			byHandle[p.Handle] = p
		}
		priv := byHandle["globex--globex-private"]
		if !priv.Private || priv.State != platform.StatePaused || priv.Currency != "EUR" {
			t.Errorf("unexpected private program: %+v", priv)
		}
	})

	t.Run("stops cleanly on repeating first ID and sets Complete=false on partial failure", func(t *testing.T) {
		var p2Fail atomic.Bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/engagements" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if r.URL.Query().Get("page") == "2" && p2Fail.Load() {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"status":400,"title":"Bad Request","message":"bad page"}`))
				return
			}
			// Always return page 1 (24 items) -> must stop on page 2 via repeating first ID!
			_, _ = w.Write(readFixture(t, "engagements_p1.json"))
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, Token: freshAccess})
		listing, err := c.ListPrograms(context.Background())
		if err != nil {
			t.Fatalf("ListPrograms repeating page: %v", err)
		}
		if !listing.Complete || len(listing.Programs) != 24 {
			t.Fatalf("got %d complete=%v, want 24 complete=true", len(listing.Programs), listing.Complete)
		}

		p2Fail.Store(true)
		partial, err := c.ListPrograms(context.Background())
		if err == nil {
			t.Fatal("expected error on page 2 failure")
		}
		if partial.Complete || len(partial.Programs) != 24 {
			t.Fatalf("expected Complete=false with 24 partial programs, got %d complete=%v", len(partial.Programs), partial.Complete)
		}
	})
}

func TestFetchScopePrefixCleanupLockedAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engagements/acme/acme-bounty":
			_, _ = w.Write(readFixture(t, "engagement_detail_acme.json"))
		case "/engagements/globex/globex-private":
			_, _ = w.Write(readFixture(t, "engagement_detail_locked.json"))
		case "/engagements/missing/none":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, Options{BaseURL: srv.URL, Token: "sample-access"})

	// Test resolveOrgSlug fallback via Handle ("org--slug") and PlatformID ("org/slug").
	entries, detail, err := c.FetchScopeDetail(context.Background(), platform.Program{Handle: "acme--acme-bounty"})
	if err != nil {
		t.Fatalf("FetchScopeDetail acme: %v", err)
	}

	byIdent := map[string]platform.ScopeEntry{}
	for _, e := range entries {
		byIdent[e.Identifier] = e
	}

	if e, ok := byIdent["app.example.com"]; !ok || !e.InScope || e.AssetType != scope.AssetDomain ||
		!strings.Contains(e.Instruction, "Website") || !strings.Contains(e.Instruction, "Main web application") {
		t.Errorf("unexpected app.example.com entry: %+v", e)
	}
	if e, ok := byIdent["id.example.com"]; !ok || !e.InScope || !strings.Contains(e.Instruction, "only misconfigurations") {
		t.Errorf("unexpected id.example.com entry: %+v", e)
	}
	if e, ok := byIdent["*.api.example.org"]; !ok || e.AssetType != scope.AssetWildcard || !e.BountyEligible {
		t.Errorf("unexpected *.api.example.org entry: %+v", e)
	}
	if _, ok := byIdent["www.example.com"]; !ok {
		t.Errorf("expected expanded www.example.com in %+v", entries)
	}
	if _, ok := byIdent["www.example.ch"]; !ok {
		t.Errorf("expected expanded www.example.ch in %+v", entries)
	}
	if e, ok := byIdent["legacy.example.com"]; !ok || e.InScope || e.BountyEligible {
		t.Errorf("unexpected out-of-scope legacy.example.com entry: %+v", e)
	}

	// Verify Markdown policy sections & bounty table.
	for _, want := range []string{
		"Markdown summary for Acme.",
		"## Scope Notes",
		"## Rules",
		"## Testing Allowed",
		"## Testing Disallowed",
		"## Qualified Vulnerabilities",
		"## Non-Qualified Vulnerabilities",
		"## Safe Harbor",
	} {
		if !strings.Contains(detail.Policy, want) {
			t.Errorf("Policy missing %q", want)
		}
	}
	if len(detail.Rewards) != 1 || detail.Rewards[0].Critical.Max != 3000 || detail.Rewards[0].Low.Min != 100 {
		t.Errorf("unexpected Rewards: %+v", detail.Rewards)
	}

	// Locked & !briefingAccessible -> ErrScopeUnavailable with empty entries.
	lockedEntries, lockedDetail, err := c.FetchScopeDetail(context.Background(), platform.Program{PlatformID: "globex/globex-private", Handle: "globex--globex-private"})
	if !errors.Is(err, platform.ErrScopeUnavailable) {
		t.Fatalf("expected ErrScopeUnavailable for locked engagement, got %v", err)
	}
	if len(lockedEntries) != 0 {
		t.Errorf("expected empty entries when locked, got %d", len(lockedEntries))
	}
	if lockedDetail.Requirements == nil || !lockedDetail.Requirements.Cleared || !lockedDetail.Requirements.Terms {
		t.Errorf("unexpected locked Requirements: %+v", lockedDetail.Requirements)
	}

	// 404 returns error (not ErrScopeUnavailable).
	if _, _, err := c.FetchScopeDetail(context.Background(), platform.Program{Handle: "missing--none"}); err == nil || errors.Is(err, platform.ErrScopeUnavailable) {
		t.Fatalf("expected non-unavailable error for 404, got %v", err)
	}
}

func TestRateLimitsAuth401RefreshAndCheckpointRollback(t *testing.T) {
	t.Run("429 and 5xx retry politely and 401 refreshes once", func(t *testing.T) {
		var engCalls, tokenCalls atomic.Int32
		expFuture := time.Now().Add(time.Hour).Unix()
		oldAccess := makeTestJWT(t, "Bearer", "sample_user", expFuture)
		newAccess := makeTestJWT(t, "Bearer", "sample_user", expFuture+600)
		refreshTok := makeTestJWT(t, "Refresh", "sample_user", expFuture+3600)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/engagements":
				n := engCalls.Add(1)
				switch n {
				case 1:
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(http.StatusTooManyRequests)
					return
				case 2:
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				case 3:
					w.WriteHeader(http.StatusUnauthorized)
					return
				default:
					if r.Header.Get("Authorization") != "Bearer "+newAccess {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					_, _ = w.Write(readFixture(t, "engagements_p2.json"))
				}
			case "/protocol/openid-connect/token":
				tokenCalls.Add(1)
				_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: newAccess, RefreshToken: refreshTok, ExpiresIn: 300})
			}
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, IDURL: srv.URL, Token: oldAccess})
		c.setTokens(oldAccess, refreshTok)
		c.http = fastTestHTTPClient(srv.URL, c.jar)

		listing, err := c.ListPrograms(context.Background())
		if err != nil {
			t.Fatalf("ListPrograms: %v", err)
		}
		if len(listing.Programs) != 2 || tokenCalls.Load() != 1 {
			t.Fatalf("unexpected programs=%d tokenCalls=%d", len(listing.Programs), tokenCalls.Load())
		}
		reqs, limited := c.Stats()
		if reqs < 4 || limited != 1 {
			t.Errorf("unexpected Stats: reqs=%d limited=%d", reqs, limited)
		}
	})

	t.Run("401 without refresh marks token credential stuck", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, Token: "expired-access"})
		if err := c.KeepAlive(context.Background()); !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("expected ErrSessionExpired, got %v", err)
		}
		creds := c.Credentials()
		if len(creds) != 1 || !creds[0].Stuck || creds[0].Failure == "" {
			t.Errorf("expected stuck refused credential, got %+v", creds)
		}
	})

	t.Run("browserSignIn protects sessionjar with Checkpoint/Rollback", func(t *testing.T) {
		dir := t.TempDir()
		jarPath := filepath.Join(dir, "gobugfree.json")
		loginState := filepath.Join(dir, "gobugfree-login.json")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(readFixture(t, "settings_ok.json"))
		}))
		defer srv.Close()

		u, _ := url.Parse(srv.URL)
		jar, err := sessionjar.Open(jarPath, u.Hostname())
		if err != nil {
			t.Fatal(err)
		}

		c := newTestClient(t, Options{
			BaseURL:    srv.URL,
			IDURL:      srv.URL,
			Email:      "sample@example.com",
			Password:   "secret",
			Jar:        jar,
			LoginState: loginState,
			WebHost:    weblogin.NewHost(weblogin.Chrome{}),
		})
		c.seedBaseCookies([]*http.Cookie{{Name: sessionCookieName, Value: "working-session-cookie", Path: "/"}})
		c.setTokens("working-access", "working-refresh")

		c.browserLoginFn = func(ctx context.Context) error {
			c.seedBaseCookies([]*http.Cookie{{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1}})
			c.setTokens("", "")
			return errors.New("CAPTCHA required on login page")
		}

		c.authMu.Lock()
		err = c.browserSignInLocked(context.Background())
		c.authMu.Unlock()
		if err == nil {
			t.Fatal("expected failed browser login error")
		}
		if !c.hasSessionCookie() {
			t.Fatal("expected sessionjar rollback to restore session cookie")
		}
		gotCookie := ""
		for _, ck := range jar.Cookies(u) {
			if ck.Name == sessionCookieName {
				gotCookie = ck.Value
			}
		}
		if gotCookie != "working-session-cookie" {
			t.Errorf("restored cookie = %q, want working-session-cookie", gotCookie)
		}
		a, r := c.tokens()
		if a != "working-access" || r != "working-refresh" {
			t.Errorf("restored tokens = (%q, %q), want working tokens", a, r)
		}
	})

	t.Run("deviceFlowLogin exchanges device code for bearer and refresh tokens", func(t *testing.T) {
		expFuture := time.Now().Add(time.Hour).Unix()
		newAccess := makeTestJWT(t, "Bearer", "sample_user", expFuture)
		newRefresh := makeTestJWT(t, "Refresh", "sample_user", expFuture+3600)
		var pollCount atomic.Int32

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/settings":
				if r.Header.Get("Authorization") == "Bearer "+newAccess {
					_, _ = w.Write(readFixture(t, "settings_ok.json"))
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte("<!DOCTYPE html><html><body>Login</body></html>"))
			case "/protocol/openid-connect/auth/device":
				_ = json.NewEncoder(w).Encode(deviceAuthResponse{
					DeviceCode:              "dev-code-123",
					UserCode:                "ABCD-EFGH",
					VerificationURI:         "https://id.example.com/device",
					VerificationURIComplete: "https://id.example.com/device?user_code=ABCD-EFGH",
					ExpiresIn:               60,
					Interval:                1,
				})
			case "/protocol/openid-connect/token":
				if pollCount.Add(1) == 1 {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
					return
				}
				_ = json.NewEncoder(w).Encode(tokenResponse{
					AccessToken:  newAccess,
					RefreshToken: newRefresh,
					ExpiresIn:    300,
				})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		c := newTestClient(t, Options{
			BaseURL:  srv.URL,
			IDURL:    srv.URL,
			Email:    "sample@example.com",
			Password: "secret",
			WebHost:  weblogin.NewHost(weblogin.Chrome{}),
		})
		var sawVerifyURL string
		c.deviceLoginFn = func(_ context.Context, verifyURL string) error {
			sawVerifyURL = verifyURL
			return nil
		}

		if err := c.deviceFlowLogin(context.Background()); err != nil {
			t.Fatalf("deviceFlowLogin: %v", err)
		}
		if sawVerifyURL != "https://id.example.com/device?user_code=ABCD-EFGH" {
			t.Errorf("unexpected verifyURL: %q", sawVerifyURL)
		}
		gotA, gotR := c.tokens()
		if gotA != newAccess || gotR != newRefresh {
			t.Errorf("got tokens (%q, %q), want (%q, %q)", gotA, gotR, newAccess, newRefresh)
		}
		if ok, _, err := c.sessionCheck(context.Background()); err != nil || !ok {
			t.Fatalf("expected sessionCheck to succeed with obtained Bearer token, ok=%v err=%v", ok, err)
		}
	})
}

func TestInvitesReportsAndStats(t *testing.T) {
	t.Run("unauthenticated invites and reports return ErrNoWebLogin", func(t *testing.T) {
		c := newTestClient(t, Options{PublicOnly: true})
		if _, err := c.ListInvites(context.Background()); !errors.Is(err, platform.ErrNoWebLogin) {
			t.Fatalf("ListInvites: got %v, want ErrNoWebLogin", err)
		}
		if err := c.AcceptInvite(context.Background(), "globex--globex-private"); !errors.Is(err, platform.ErrNoWebLogin) {
			t.Fatalf("AcceptInvite: got %v, want ErrNoWebLogin", err)
		}
		if c.ReportsConfigured() {
			t.Fatal("expected ReportsConfigured=false in public mode")
		}
		if _, err := c.FetchReports(context.Background(), nil, 10, nil); !errors.Is(err, platform.ErrNoWebLogin) {
			t.Fatalf("FetchReports: got %v, want ErrNoWebLogin", err)
		}
	})

	t.Run("invites list+accept, reports+payouts metadata-only, and stats", func(t *testing.T) {
		var accepted atomic.Bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/comments") || strings.Contains(r.URL.Path, "/writeup") {
				t.Fatalf("PRIVACY VIOLATION: fetched %s", r.URL.Path)
			}
			switch {
			case r.URL.Path == "/engagements":
				_, _ = w.Write(readFixture(t, "engagements_p2.json"))
			case r.Method == http.MethodPost && r.URL.Path == "/engagements/globex/globex-private/invitation/accept":
				accepted.Store(true)
				_, _ = w.Write([]byte(`{"state":"accepted"}`))
			case r.URL.Path == "/payouts":
				_, _ = w.Write(readFixture(t, "payouts.json"))
			case r.URL.Path == "/reports":
				_, _ = w.Write(readFixture(t, "reports_p1.json"))
			case r.URL.Path == "/engagements/acme/acme-bounty":
				_, _ = w.Write(readFixture(t, "engagement_detail_acme.json"))
			case r.URL.Path == "/engagements/gone/missing":
				w.WriteHeader(http.StatusNotFound)
			default:
				t.Errorf("unexpected %s %s", r.Method, r.URL.String())
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, Token: "sample-access"})

		invites, err := c.ListInvites(context.Background())
		if err != nil {
			t.Fatalf("ListInvites: %v", err)
		}
		if len(invites) != 1 || invites[0].Handle != "globex--globex-private" {
			t.Fatalf("unexpected invites: %+v", invites)
		}
		if err := c.AcceptInvite(context.Background(), "globex--globex-private"); err != nil {
			t.Fatalf("AcceptInvite: %v", err)
		}
		if !accepted.Load() {
			t.Fatal("expected POST invitation/accept")
		}

		if !c.ReportsConfigured() {
			t.Fatal("expected ReportsConfigured=true")
		}
		var saved []platform.Report
		complete, err := c.FetchReports(context.Background(), nil, 50, func(_ context.Context, batch []platform.Report) error {
			saved = append(saved, batch...)
			return nil
		})
		if err != nil || !complete {
			t.Fatalf("FetchReports: complete=%v err=%v", complete, err)
		}
		if len(saved) != 3 {
			t.Fatalf("got %d reports, want 3", len(saved))
		}
		byKey := map[string]platform.Report{}
		for _, r := range saved {
			byKey[r.Key] = r
		}
		r1 := byKey["00000000-0000-4000-8000-000000000101"]
		if r1.ProgramHandle != "acme--acme-bounty" || r1.State != platform.ReportAccepted ||
			r1.Severity != platform.SeverityMedium || r1.SubmittedSeverity != platform.SeverityHigh || !r1.SeverityChanged ||
			r1.CVSS == nil || *r1.CVSS != 6.1 {
			t.Errorf("unexpected report 101: %+v", r1)
		}
		if !r1.PaymentsKnown || len(r1.Payments) != 1 || r1.Payments[0].Amount != 1000 || r1.Payments[0].Currency != "CHF" {
			t.Errorf("unexpected report 101 payments: %+v", r1.Payments)
		}
		if byKey["00000000-0000-4000-8000-000000000102"].State != platform.ReportDuplicate {
			t.Errorf("expected duplicate state for 102, got %q", byKey["00000000-0000-4000-8000-000000000102"].State)
		}
		if byKey["00000000-0000-4000-8000-000000000103"].State != platform.ReportNotApplicable {
			t.Errorf("expected not_applicable state for 103, got %q", byKey["00000000-0000-4000-8000-000000000103"].State)
		}

		// StatsFetcher
		statsMap := map[string]platform.ProgramStats{}
		err = c.FetchProgramStats(context.Background(), []string{"acme--acme-bounty", "gone--missing"}, func(_ []string, st map[string]platform.ProgramStats) error {
			for k, v := range st {
				statsMap[k] = v
			}
			return nil
		})
		if err != nil {
			t.Fatalf("FetchProgramStats: %v", err)
		}
		st, ok := statsMap["acme--acme-bounty"]
		if !ok {
			t.Fatal("expected stats for acme--acme-bounty")
		}
		if st.HackersThanked == nil || *st.HackersThanked != 2 || st.Hunters == nil || *st.Hunters != 2 {
			t.Errorf("unexpected HackersThanked/Hunters: %v / %v", st.HackersThanked, st.Hunters)
		}
		if st.Currency != "CHF" || len(st.Rewards) != 1 {
			t.Errorf("unexpected Currency/Rewards: %q / %+v", st.Currency, st.Rewards)
		}
		if st.Profile.Introduction == "" || st.Profile.InScope == "" || st.Profile.OutScope == "" {
			t.Errorf("unexpected empty Profile fields: %+v", st.Profile)
		}
		if st.Facts == nil || st.Facts.SafeHarbor != "full" || st.Requirements == nil || !st.Requirements.IDVerified || !st.Requirements.Terms {
			t.Errorf("unexpected Facts/Requirements: %+v / %+v", st.Facts, st.Requirements)
		}
		if _, exists := statsMap["gone--missing"]; exists {
			t.Error("expected missing program to be skipped in statsMap")
		}
	})
}

func fastTestHTTPClient(baseRaw string, jar http.CookieJar) *httpclient.Client {
	u, _ := url.Parse(baseRaw)
	return httpclient.New(httpclient.Options{
		RatePerSecond: 100,
		Burst:         5,
		Jar:           jar,
		RedirectHosts: []string{u.Host},
		Sleep:         func(context.Context, time.Duration) error { return nil },
	})
}
