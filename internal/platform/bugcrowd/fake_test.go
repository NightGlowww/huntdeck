package bugcrowd

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/totp"
)

const (
	testEmail    = "hunter@example.com"
	testPassword = "p#ss$word!"
	testSecret   = "JBSWY3DPEHPK3PXP"
	goodSession  = "good-session"
)

var testNow = time.Date(2026, 10, 26, 12, 0, 5, 0, time.UTC)

// fakeBugcrowd serves bugcrowd.com, identity.bugcrowd.com and Okta from
// three test servers, with fixtures from testdata/.
type fakeBugcrowd struct {
	t                    *testing.T
	base, identity, okta *httptest.Server

	mu            sync.Mutex
	hits          map[string]int
	sessionChecks int
	// expireAfterChecks, if > 0, makes the session invalid after that many
	// /auth/session calls.
	expireAfterChecks int
	blockAll          bool
	passwordsTried    int
	// otpStep is the fixture asking for the second factor (okta_otp.json
	// by default).
	otpStep string
	// invitesFail makes the invitations answer 500; accepted are the
	// invitations accepted, by ID.
	invitesFail bool
	accepted    []string
}

func newFake(t *testing.T) *fakeBugcrowd {
	t.Helper()
	f := &fakeBugcrowd{t: t, hits: map[string]int{}}
	f.base = httptest.NewServer(http.HandlerFunc(f.serveBase))
	f.identity = httptest.NewServer(http.HandlerFunc(f.serveIdentity))
	f.okta = httptest.NewServer(http.HandlerFunc(f.serveOkta))
	t.Cleanup(func() { f.base.Close(); f.identity.Close(); f.okta.Close() })
	return f
}

func (f *fakeBugcrowd) client(t *testing.T, o Options) *Client {
	t.Helper()
	o.BaseURL, o.IdentityURL, o.OktaURL = f.base.URL, f.identity.URL, f.okta.URL
	if o.Now == nil {
		o.Now = func() time.Time { return testNow }
	}
	o.HTTP = httpclient.Options{RatePerSecond: 1000, Burst: 100, MaxAttempts: 2,
		Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() }}
	c, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *fakeBugcrowd) hit(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func (f *fakeBugcrowd) fixture(name string) []byte {
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		f.t.Fatal(err)
	}
	return []byte(strings.ReplaceAll(string(b), "{{okta}}", f.okta.URL))
}

func (f *fakeBugcrowd) write(w http.ResponseWriter, ctype, name string) {
	w.Header().Set("Content-Type", ctype)
	_, _ = w.Write(f.fixture(name))
}

func (f *fakeBugcrowd) loggedIn(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	return err == nil && c.Value == goodSession
}

func (f *fakeBugcrowd) serveBase(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits[r.URL.Path]++
	blocked := f.blockAll
	f.mu.Unlock()
	if blocked {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html>Access denied</html>"))
		return
	}
	const js = "application/json; charset=utf-8"
	switch r.URL.Path {
	case "/auth/session":
		f.mu.Lock()
		f.sessionChecks++
		expired := f.expireAfterChecks > 0 && f.sessionChecks > f.expireAfterChecks
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if !f.loggedIn(r) || expired {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Forbidden"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"u-1","saml":false,"secondsRemaining":1209600}`))
	case "/engagements.json":
		q := r.URL.Query()
		switch {
		case q.Get("sort_by") == "starts" && q.Get("sort_direction") == "desc" && q.Get("page") == "1":
			f.write(w, js, "engagements_page1.json") // stands in for the newest page
		case q.Get("sort_by") != "name":
			f.t.Errorf("listing not sorted by name: %s", r.URL.RawQuery)
		default:
			f.write(w, js, "engagements_page"+q.Get("page")+".json")
		}
	case "/engagements/invites.json":
		f.mu.Lock()
		fail := f.invitesFail
		f.mu.Unlock()
		q := r.URL.Query()
		switch {
		case !f.loggedIn(r):
			http.Redirect(w, r, "/user/sign_in", http.StatusFound)
		case fail:
			http.Error(w, "boom", http.StatusInternalServerError)
		case q.Get("status") != "pending":
			f.t.Errorf("invitations asked for status %q", q.Get("status"))
		case q.Get("page") == "1":
			f.write(w, js, "invites_pending.json")
		default:
			w.Header().Set("Content-Type", js)
			_, _ = w.Write([]byte(`{"invites":[],"paginationMeta":{"limit":50,"totalCount":2}}`))
		}
	case "/engagements/invites":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><meta name="csrf-param" content="authenticity_token"><meta name="csrf-token" content="tok-123"></head><body></body></html>`))
	case "/engagements/1a000000-0000-4000-8000-000000000001/accept.json":
		switch {
		case r.Method != http.MethodPost:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		case !f.loggedIn(r):
			http.Redirect(w, r, "/user/sign_in", http.StatusFound)
		case r.Header.Get("X-CSRF-TOKEN") != "tok-123":
			http.Error(w, `{"errors":[{"title":"Invalid authenticity token"}]}`, http.StatusUnprocessableEntity)
		default:
			f.mu.Lock()
			f.accepted = append(f.accepted, "1a000000-0000-4000-8000-000000000001")
			f.mu.Unlock()
			w.Header().Set("Content-Type", js)
			_, _ = w.Write([]byte(`{}`))
		}
	case "/submissions.json":
		if !f.loggedIn(r) {
			http.Redirect(w, r, "/user/sign_in", http.StatusFound)
			return
		}
		f.write(w, js, "submissions_"+r.URL.Query().Get("offset[]")+".json")
	case "/submissions/aaaaaaaa-0000-4000-8000-000000000003":
		f.write(w, js, "submission_3.json")
	case "/submissions/aaaaaaaa-0000-4000-8000-000000000002":
		f.write(w, js, "submission_2.json")
	case "/submissions/aaaaaaaa-0000-4000-8000-000000000001":
		f.write(w, js, "submission_1.json")
	case "/acme/submissions/r3/activities":
		f.write(w, js, "activities_3.json")
	case "/acme/submissions/r2/activities":
		f.write(w, js, "activities_2.json")
	case "/engagements/acme-bb/changelog.json":
		f.write(w, js, "changelog_acme.json")
	case "/engagements/acme-bb/changelog/0d5e0c1a-1111-4aaa-8bbb-000000000002.json":
		f.write(w, js, "document_acme.json")
	case "/engagements/acme-bb/changelog/0d5e0c1a-1111-4aaa-8bbb-000000000001.json":
		f.write(w, js, "document_acme_v1.json")
	case "/engagements/globex-vdp/changelog.json", "/engagements/initech-pt/changelog.json":
		w.Header().Set("Content-Type", js)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[{"detail":"Not found","title":"Not found"}]}`))
	case "/engagements/acme-bb/crowdstream.json":
		f.write(w, js, "crowdstream_acme_"+r.URL.Query().Get("page")+".json")
	case "/crowdstream.json":
		f.write(w, js, "crowdstream_all_"+r.URL.Query().Get("page")+".json")
	case "/engagements/acme-bb/statistics":
		w.Header().Set("Content-Type", js)
		_, _ = w.Write([]byte(`{"rewardedVulnerabilities":643,"averagePayout":"$1,569.04","validSubmissionCount":null,"validationWithin":"6 days"}`))
	case "/engagements/acme-bb/recently_joined_users.json":
		w.Header().Set("Content-Type", js)
		_, _ = w.Write([]byte(`{"users":[{"id":"u1","username":"researcher1","avatar":"https://example.com/a.png","accentColor":null,"profileUrl":"/h/researcher1"}],"total":544}`))
	case "/engagements/acme-bb/hall_of_fames.json":
		w.Header().Set("Content-Type", js)
		_, _ = w.Write([]byte(`{"presented_heroes":[{"username":"researcher1"}],"pagination_meta":{"totalCount":578,"limit":100}}`))
	case "/engagements/acme-bb/engagement_known_issues.json":
		w.Header().Set("Content-Type", js)
		_, _ = w.Write([]byte(`{"total":12,"unique":9}`))
	case "/engagements/acme-bb/scope_ranks":
		w.Header().Set("Content-Type", js)
		_, _ = w.Write([]byte(`{"scopeRank":4}`))
	case "/engagements/acme-bb/announcements.json":
		w.Header().Set("Content-Type", js)
		_, _ = w.Write([]byte(`{"announcements":[{"id":"a2","title":" Bonus week ","body":"<p>Double bounties on <strong>api.example.com</strong>.</p>","publishedAt":"2026-10-20T10:00:00.000Z","publishedBy":{"name":"Staff","isStaff":true}},{"id":"","title":"no id","body":"","publishedAt":"2026-10-01T10:00:00Z"}]}`))
	case "/engagements/globex-vdp/announcements.json":
		w.Header().Set("Content-Type", js)
		_, _ = w.Write([]byte(`{"announcements":[]}`))
	case "/engagements/globex-vdp/statistics":
		w.Header().Set("Content-Type", js)
		_, _ = w.Write([]byte(`{"rewardedVulnerabilities":null,"averagePayout":null,"validSubmissionCount":109,"validationWithin":null}`))
	case "/engagements/initech-pt/statistics":
		w.Header().Set("Content-Type", js)
		_, _ = w.Write([]byte(`{"rewardedVulnerabilities":null,"averagePayout":null,"validSubmissionCount":null,"validationWithin":null}`))
	case "/engagements/globex-vdp":
		f.write(w, "text/html; charset=utf-8", "brief_globex.html")
	case "/engagements/globex-vdp/changelog/9f000000-0000-4000-8000-000000000007.json":
		f.write(w, js, "document_globex.json")
	case "/engagements/initech-pt":
		f.write(w, "text/html; charset=utf-8", "brief_compliance.html")
	case "/engagements/umbrella-vdp":
		http.Redirect(w, r, "/engagements/umbrella-vdp/migrated", http.StatusFound)
	case "/engagements/umbrella-vdp/migrated":
		f.write(w, "text/html; charset=utf-8", "brief_migrated.html")
	case "/hooli/target_groups":
		f.write(w, js, "legacy_groups.json")
	case "/hooli/target_groups/1/targets":
		f.write(w, js, "legacy_targets_1.json")
	case "/hooli/target_groups/2/targets":
		f.write(w, js, "legacy_targets_2.json")
	case "/dashboard":
		if !f.loggedIn(r) {
			http.Redirect(w, r, f.identity.URL+"/login?user_hint=researcher&returnTo=%2Fdashboard", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("<html>dashboard</html>"))
	case "/auth/set-session":
		if r.URL.Query().Get("t") != "handoff" {
			http.Error(w, "bad handoff", http.StatusBadRequest)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: goodSession, Path: "/", HttpOnly: true})
		http.Redirect(w, r, "/dashboard", http.StatusFound)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeBugcrowd) serveIdentity(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/login/hacker/oauth2/authorization/hacker":
		http.Redirect(w, r, f.okta.URL+"/oauth2/default/v1/authorize?response_type=code&state=s1", http.StatusFound)
	case "/login/oauth2/code/hacker":
		if r.URL.Query().Get("code") != "auth-code" {
			http.Error(w, "bad code", http.StatusBadRequest)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "_bugcrowd_session", Value: "identity-session", Path: "/"})
		http.Redirect(w, r, "/login/signed-in", http.StatusFound)
	case "/login/signed-in":
		_, _ = w.Write([]byte("<html><script>location='//identity'</script></html>"))
	case "/login":
		if c, err := r.Cookie("_bugcrowd_session"); err != nil || c.Value != "identity-session" {
			http.Error(w, "not signed in to identity", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, f.base.URL+"/auth/set-session?t=handoff", http.StatusFound)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeBugcrowd) serveOkta(w http.ResponseWriter, r *http.Request) {
	const ion = "application/ion+json; okta-version=1.0.0"
	var body map[string]any
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
	}
	passcode := func() string {
		cr, _ := body["credentials"].(map[string]any)
		s, _ := cr["passcode"].(string)
		return s
	}
	switch r.URL.Path {
	case "/oauth2/default/v1/authorize":
		f.write(w, "text/html", "okta_authorize.html")
	case "/idp/idx/introspect":
		if body["stateToken"] != "st-123-abc" {
			f.t.Errorf("introspect stateToken = %v", body["stateToken"])
		}
		f.write(w, ion, "okta_introspect.json")
	case "/idp/idx/identify":
		if body["identifier"] != testEmail || body["stateHandle"] != "sh-1" {
			f.t.Errorf("identify body = %v", body)
		}
		f.write(w, ion, "okta_password.json")
	case "/idp/idx/challenge/answer":
		want, _ := totp.Code(testSecret, testNow)
		switch {
		case body["stateHandle"] == "sh-2" && passcode() == testPassword:
			f.write(w, ion, cmp.Or(f.otpStep, "okta_otp.json"))
		case body["stateHandle"] == "sh-2":
			f.mu.Lock()
			f.passwordsTried++
			f.mu.Unlock()
			w.Header().Set("Content-Type", ion)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write(f.fixture("okta_error.json"))
		case body["stateHandle"] == "sh-3" && passcode() == want:
			f.write(w, ion, "okta_success.json")
		default:
			f.t.Errorf("unexpected answer: handle=%v", body["stateHandle"])
			http.Error(w, "bad", http.StatusBadRequest)
		}
	case "/login/token/redirect":
		http.Redirect(w, r, f.identity.URL+"/login/oauth2/code/hacker?code=auth-code&state=s1", http.StatusFound)
	default:
		http.NotFound(w, r)
	}
}

func program(f *fakeBugcrowd, handle, path string) platform.Program {
	return platform.Program{Handle: handle, URL: f.base.URL + path}
}

// bountyRange formats a program's bounty range for comparisons.
func bountyRange(p platform.Program) string {
	v := func(f *float64) string {
		if f == nil {
			return "-"
		}
		return strconv.FormatFloat(*f, 'f', -1, 64)
	}
	return v(p.BountyMin) + ".." + v(p.BountyMax)
}
