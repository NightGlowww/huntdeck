package vulbox

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

	"github.com/mdpsec/huntdeck/internal/platform"
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

// makeTestJWT constructs a synthetic HS256-shaped JWT with the given exp claim.
func makeTestJWT(t *testing.T, exp int64) string {
	t.Helper()
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(map[string]any{"uid": 10001, "username": "sample_hunter", "exp": exp})
	if err != nil {
		t.Fatal(err)
	}
	pl := base64.RawURLEncoding.EncodeToString(payload)
	sig := base64.RawURLEncoding.EncodeToString([]byte("synthetic-test-signature"))
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

func TestCredentialsAndJWTExpiry(t *testing.T) {
	expUnix := int64(1893456000) // 2030-01-01T00:00:00Z
	jwt := makeTestJWT(t, expUnix)

	tests := []struct {
		name     string
		opts     Options
		wantLen  int
		wantName string
		wantExp  bool
		wantAcct string
	}{
		{
			name:     "public only returns nil credentials",
			opts:     Options{PublicOnly: true, Session: jwt, Account: "user@example.com", Password: "secret"},
			wantLen:  0,
			wantAcct: "",
		},
		{
			name:     "session bearer parses jwt exp",
			opts:     Options{Session: "Bearer " + jwt},
			wantLen:  1,
			wantName: "Session cookie",
			wantExp:  true,
		},
		{
			name:     "session cookie header parses jwt exp",
			opts:     Options{Session: "Cookie: vulbox_token=" + jwt + "; uuid=abc123"},
			wantLen:  1,
			wantName: "Session cookie",
			wantExp:  true,
		},
		{
			name:     "website login uses exact Website login name",
			opts:     Options{Account: "SampleUser@Example.COM", Password: "secret", WebHost: weblogin.NewHost(weblogin.Chrome{})},
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
			if tc.wantExp {
				if creds[0].Expires == nil || creds[0].Expires.Unix() != expUnix {
					t.Errorf("Credential.Expires = %v, want unix %d", creds[0].Expires, expUnix)
				}
			}
		})
	}
}

func TestListProgramsAuthenticatedMergeAndPagination(t *testing.T) {
	var acceptedInvite atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Errorf("missing Authorization header on %s", r.URL.Path)
		}
		if r.Header.Get("X-Client-Type") != "web" {
			t.Errorf("missing X-Client-Type header on %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/web/account/basic-info":
			_, _ = w.Write(readFixture(t, "basic_info_ok.json"))
		case "/hacker/project/mine/list":
			_, _ = w.Write(readFixture(t, "mine_list.json"))
		case "/hacker/project/projects":
			if r.URL.Query().Get("page") == "2" {
				_, _ = w.Write(readFixture(t, "projects_p2.json"))
			} else {
				_, _ = w.Write(readFixture(t, "projects_p1.json"))
			}
		case "/web/project/enterprise/src":
			_, _ = w.Write(readFixture(t, "enterprise_src.json"))
		case "/hacker/project/invite":
			if r.Method != http.MethodPost {
				t.Errorf("expected POST on /hacker/project/invite")
			}
			var body struct {
				ProjectID  int64  `json:"project_id"`
				Status     int    `json:"status"`
				InviteCode string `json:"invite_code"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.ProjectID == 20002 && body.Status == 1 && body.InviteCode == "INV-SAMPLE-01" {
				acceptedInvite.Add(1)
			}
			_, _ = w.Write([]byte(`{"code":200,"data":{},"msg":"success"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, Options{
		BaseURL:  srv.URL,
		Session:  makeTestJWT(t, 1893456000),
		AutoJoin: true,
	})

	listing, err := c.ListPrograms(context.Background())
	if err != nil {
		t.Fatalf("ListPrograms: %v", err)
	}
	if !listing.Complete {
		t.Fatalf("expected Complete=true")
	}
	// mine/list (20001, 20002) + projects (10001, 10002) + enterprise/src (10001 deduped, 10003) = 5 unique programs
	if len(listing.Programs) != 5 {
		t.Fatalf("got %d programs, want 5", len(listing.Programs))
	}
	if acceptedInvite.Load() != 1 {
		t.Errorf("expected AutoJoin to accept invite 20002 once, got %d", acceptedInvite.Load())
	}

	byHandle := map[string]platform.Program{}
	for _, p := range listing.Programs {
		byHandle[p.Handle] = p
	}
	if !byHandle["20001"].Private {
		t.Errorf("expected 20001 to be Private")
	}
	if byHandle["10001"].Private {
		t.Errorf("expected 10001 (Enterprise SRC) to be public")
	}
	if byHandle["10001"].Currency != "CNY" || byHandle["10001"].BountyMax == nil || *byHandle["10001"].BountyMax != 10000 {
		t.Errorf("unexpected 10001 bounty: cur=%q max=%v", byHandle["10001"].Currency, byHandle["10001"].BountyMax)
	}
	if byHandle["10002"].State != platform.StatePaused {
		t.Errorf("expected 10002 state=paused, got %q", byHandle["10002"].State)
	}
	if byHandle["10003"].State != platform.StateClosed {
		t.Errorf("expected 10003 state=closed, got %q", byHandle["10003"].State)
	}
}

func TestListProgramsPartialFailureAndMidPollSessionExpiry(t *testing.T) {
	t.Run("partial page failure sets Complete=false", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/web/project/projects":
				if r.URL.Query().Get("page") == "2" {
					_, _ = w.Write([]byte(`{"code":400,"data":{},"msg":"bad page"}`))
					return
				}
				_, _ = w.Write(readFixture(t, "projects_p1.json"))
			default:
				_, _ = w.Write(readFixture(t, "enterprise_src.json"))
			}
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, PublicOnly: true})
		listing, err := c.ListPrograms(context.Background())
		if err == nil {
			t.Fatal("expected error on page 2 failure")
		}
		if listing.Complete {
			t.Fatal("expected Complete=false on partial failure")
		}
		if len(listing.Programs) != 1 {
			t.Fatalf("expected 1 partial program, got %d", len(listing.Programs))
		}
	})

	t.Run("session expires mid-listing sets Complete=false", func(t *testing.T) {
		var basicCalls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/web/account/basic-info":
				if basicCalls.Add(1) == 1 {
					_, _ = w.Write(readFixture(t, "basic_info_ok.json"))
				} else {
					_, _ = w.Write(readFixture(t, "basic_info_logged_out.json"))
				}
			case "/hacker/project/mine/list":
				_, _ = w.Write(readFixture(t, "mine_list.json"))
			case "/hacker/project/projects":
				_, _ = w.Write(readFixture(t, "projects_p2.json"))
			case "/web/project/enterprise/src":
				_, _ = w.Write(readFixture(t, "enterprise_src.json"))
			}
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, Session: makeTestJWT(t, 1893456000)})
		listing, err := c.ListPrograms(context.Background())
		if !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("expected ErrSessionExpired, got %v", err)
		}
		if listing.Complete {
			t.Fatal("expected Complete=false when session ended mid-listing")
		}
	})
}

func TestEnvelopeErrorCodes429And401And403004(t *testing.T) {
	t.Run("HTTP 200 code 429 retries politely and records rate limit", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) <= 2 {
				_, _ = w.Write([]byte(`{"code":429,"data":{},"msg":"Too Many Request."}`))
				return
			}
			_, _ = w.Write(readFixture(t, "basic_info_ok.json"))
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, Session: "sample-token"})
		ok, err := c.sessionValid(context.Background())
		if err != nil || !ok {
			t.Fatalf("expected success after 429 retries, got ok=%v err=%v", ok, err)
		}
		if calls.Load() != 3 {
			t.Errorf("expected 3 attempts, got %d", calls.Load())
		}
		_, limited := c.Stats()
		if limited != 2 {
			t.Errorf("expected Stats() rateLimited=2, got %d", limited)
		}
	})

	t.Run("HTTP 200 code 401004 marks pasted session expired and refused", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"code":401004,"data":{},"msg":"session kicked"}`))
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, Session: "sample-token"})
		err := c.getJSON(context.Background(), "/hacker/project/detail?id=10001", nil, nil)
		if !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("expected ErrSessionExpired, got %v", err)
		}
		creds := c.Credentials()
		if len(creds) != 1 || !creds[0].Stuck || creds[0].Failure == "" {
			t.Errorf("expected refused stuck credential, got %+v", creds)
		}
	})

	t.Run("HTTP 200 code 403004 returns ErrMFARequired", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"code":403004,"data":{},"msg":"mfa required"}`))
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, Session: "sample-token"})
		err := c.getJSON(context.Background(), "/hacker/project/detail?id=10001", nil, nil)
		if !errors.Is(err, ErrMFARequired) {
			t.Fatalf("expected ErrMFARequired, got %v", err)
		}
	})
}

func TestBrowserSignInSessionJarCheckpointRollback(t *testing.T) {
	dir := t.TempDir()
	jarPath := filepath.Join(dir, "vulbox.json")
	loginState := filepath.Join(dir, "vulbox-login.json")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(readFixture(t, "basic_info_ok.json"))
	}))
	defer srv.Close()

	jar, err := sessionjar.Open(jarPath, "vapi.vulbox.com", "www.vulbox.com", "user.vulbox.com", "vulbox.com")
	if err != nil {
		t.Fatal(err)
	}
	c := newTestClient(t, Options{
		BaseURL:    srv.URL,
		Account:    "sample@example.com",
		Password:   "sample-pw",
		Jar:        jar,
		LoginState: loginState,
		WebHost:    weblogin.NewHost(weblogin.Chrome{}),
	})
	oldToken := makeTestJWT(t, 1893456000)
	c.seedCookies([]*http.Cookie{{Name: tokenCookie, Value: oldToken, Path: "/"}})

	// Simulate a failed login attempt that wipes/corrupts cookies mid-attempt.
	c.browserLoginFn = func(ctx context.Context) error {
		c.seedCookies([]*http.Cookie{{Name: tokenCookie, Value: "", Path: "/", MaxAge: -1}})
		return errors.New("CAPTCHA required in browser")
	}

	err = c.browserSignIn(context.Background())
	if err == nil {
		t.Fatal("expected error from failed browser login")
	}
	// Rollback must restore the previous working token in memory and on disk!
	if got := c.currentToken(); got != oldToken {
		t.Fatalf("expected sessionjar rollback to restore old token, got %q", got)
	}
}

func TestParseJWTMetaAndSeedCookiesAndHeaders(t *testing.T) {
	t.Run("parseJWTMeta extracts iat and exp and rejects malformed inputs", func(t *testing.T) {
		hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
		pl, _ := json.Marshal(map[string]any{"iat": 1700000123, "exp": 1893456000})
		tok := hdr + "." + base64.RawURLEncoding.EncodeToString(pl) + "." + base64.RawURLEncoding.EncodeToString([]byte("sig"))

		iat, exp := parseJWTMeta(tok)
		if iat != 1700000123 || exp != 1893456000 {
			t.Fatalf("got iat=%d exp=%d, want 1700000123 / 1893456000", iat, exp)
		}
		for _, bad := range []string{"", "not-a-jwt", "a.b", "a.!!!.c"} {
			if gi, ge := parseJWTMeta(bad); gi != 0 || ge != 0 {
				t.Errorf("parseJWTMeta(%q) = (%d,%d), want (0,0)", bad, gi, ge)
			}
		}
	})

	t.Run("seedCookies replaces stale domain and host vulbox_token with single fresh entry", func(t *testing.T) {
		c := newTestClient(t, Options{})
		// Seed an old domain cookie directly on root and host cookie on base.
		c.jar.SetCookies(c.root, []*http.Cookie{{Name: tokenCookie, Value: "old-domain-tok", Domain: "vulbox.com", Path: "/"}})
		c.jar.SetCookies(c.base, []*http.Cookie{{Name: tokenCookie, Value: "old-host-tok", Path: "/"}})

		fresh := makeTestJWT(t, 1893456999)
		c.seedCookies([]*http.Cookie{{Name: tokenCookie, Value: fresh, Path: "/"}})

		if got := c.jarTokenCount(); got != 1 {
			t.Fatalf("jarTokenCount() = %d, want 1", got)
		}
		if got := c.currentToken(); got != fresh {
			t.Fatalf("currentToken() = %q, want %q", got, fresh)
		}
	})

	t.Run("attachHeaders aligns Origin, Referer, X-Requested-With, and synced User-Agent per host", func(t *testing.T) {
		c := newTestClient(t, Options{Session: "sample-tok"})
		c.setUserAgent("Mozilla/5.0 (CustomChromeTest)")

		vapiReq, _ := http.NewRequest(http.MethodGet, "https://vapi.vulbox.com/web/account/basic-info", nil)
		c.attachHeaders(vapiReq, false, nil)
		if got := vapiReq.Header.Get("User-Agent"); got != "Mozilla/5.0 (CustomChromeTest)" {
			t.Errorf("vapi UA = %q", got)
		}
		if got := vapiReq.Header.Get("Origin"); got != "https://www.vulbox.com" {
			t.Errorf("vapi Origin = %q, want https://www.vulbox.com", got)
		}
		if got := vapiReq.Header.Get("Referer"); got != "https://www.vulbox.com/" {
			t.Errorf("vapi Referer = %q, want https://www.vulbox.com/", got)
		}
		if got := vapiReq.Header.Get("X-Requested-With"); got != "XMLHttpRequest" {
			t.Errorf("vapi X-Requested-With = %q, want XMLHttpRequest", got)
		}

		userReq, _ := http.NewRequest(http.MethodGet, "https://user.vulbox.com/api/web/account/basic-info", nil)
		c.attachHeaders(userReq, false, nil)
		if got := userReq.Header.Get("Origin"); got != "https://user.vulbox.com" {
			t.Errorf("user Origin = %q, want https://user.vulbox.com", got)
		}
		if got := userReq.Header.Get("Referer"); got != "https://user.vulbox.com/" {
			t.Errorf("user Referer = %q, want https://user.vulbox.com/", got)
		}
		if got := userReq.Header.Get("X-Requested-With"); got != "" {
			t.Errorf("user X-Requested-With = %q, want empty", got)
		}

		userBase, _ := url.Parse("https://user.vulbox.com/api")
		c.setAPIBase(userBase)
		if c.apiBase().String() != "https://user.vulbox.com/api" {
			t.Errorf("apiBase = %s, want https://user.vulbox.com/api", c.apiBase())
		}
	})

	t.Run("auth failure surfaces envelope msg alongside ErrSessionExpired", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"code":401,"data":{},"msg":"Token invalid"}`))
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, Session: "sample-token"})
		err := c.getJSON(context.Background(), "/hacker/user/account/info", nil, nil)
		if !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("expected ErrSessionExpired, got %v", err)
		}
		if !strings.Contains(err.Error(), "Token invalid") {
			t.Fatalf("expected error to include envelope msg, got %v", err)
		}
	})
}

func TestValidateCapturedSession(t *testing.T) {
	if err := validateCapturedSession("", "", "https://www.vulbox.com/account/login", false, false); err == nil {
		t.Fatal("expected error when vulbox_token is empty")
	}
	if err := validateCapturedSession("tok-1", "tok-1", "https://user.vulbox.com/home", true, true); err == nil {
		t.Fatal("expected error when vulbox_token matches stale token")
	}
	if err := validateCapturedSession("tok-2", "tok-1", "https://www.vulbox.com/account/login", false, false); err == nil {
		t.Fatal("expected error when neither browser probe is logged in")
	}
	if err := validateCapturedSession("tok-2", "tok-1", "https://user.vulbox.com/home", true, false); err != nil {
		t.Fatalf("unexpected error for valid captured session: %v", err)
	}
}
