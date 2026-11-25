package intigriti

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// fakeWeb is the login site and web app in one server. A sign-on cookie
// ("sso") gets an app session through two form_post round trips; the API
// wants the session cookie, and POSTs the CSRF token too.
type fakeWeb struct {
	mu       sync.Mutex
	srv      *httptest.Server
	sessions int  // app sessions started
	expire   bool // refuse the next API call with 401, as an expired session
	ssoValid bool
	hits     map[string]int
	// noUpdates serves acme's page saying it has no updates.
	noUpdates bool
}

func (f *fakeWeb) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	cookie := func(r *http.Request, name string) string {
		c, err := r.Cookie(name)
		if err != nil {
			return ""
		}
		return c.Value
	}
	set := func(w http.ResponseWriter, name, value string) {
		http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true})
	}
	formPost := func(w http.ResponseWriter, action string) {
		fmt.Fprintf(w, `<html><body onload="document.forms[0].submit()"><form method='post' action='%s/%s'>
<input type='hidden' name='code' value='c0de' />
<input type='hidden' name='state' value='st&amp;ate' /></form></body></html>`, f.srv.URL, action)
	}
	mux.HandleFunc("/auth/researcher", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case cookie(r, appSessionCookie) != "":
			http.Redirect(w, r, "/researcher", http.StatusFound)
		case cookie(r, "init") != "":
			http.Redirect(w, r, "/connect/authorize?step=2", http.StatusFound)
		default:
			http.Redirect(w, r, "/connect/authorize?step=1", http.StatusFound)
		}
	})
	mux.HandleFunc("/connect/authorize", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		valid := f.ssoValid && cookie(r, "sso") != ""
		f.mu.Unlock()
		if !valid {
			fmt.Fprint(w, `<form method="post" action="/Account/Login"><input name="Input.Email" type="email"></form>`)
			return
		}
		if r.URL.Query().Get("step") == "1" {
			formPost(w, "signin-oidc")
		} else {
			formPost(w, "signin-oidc-researcher")
		}
	})
	mux.HandleFunc("/signin-oidc", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.FormValue("code") != "c0de" || r.FormValue("state") != "st&ate" {
			t.Errorf("signin-oidc got %s %v", r.Method, r.Form)
		}
		set(w, "init", "1")
		http.Redirect(w, r, "/auth/researcher", http.StatusFound)
	})
	mux.HandleFunc("/signin-oidc-researcher", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.sessions++
		n := f.sessions
		f.mu.Unlock()
		set(w, appSessionCookie, fmt.Sprint("s", n))
		set(w, csrfCookie, url.QueryEscape("csrf/"+fmt.Sprint(n)))
		http.Redirect(w, r, "/auth/researcher", http.StatusFound)
	})
	mux.HandleFunc("/api/v1/payouts", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits[r.Method+" "+r.URL.Path]++
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		b, _ := os.ReadFile(filepath.Join("testdata", "api_payouts.json"))
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/researcher", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html>app</html>") })
	mux.HandleFunc(webAPI+"/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits[r.Method+" "+r.URL.Path]++
		expire := f.expire
		f.expire = false
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "" {
			t.Errorf("%s got the API token", r.URL.Path)
		}
		if cookie(r, appSessionCookie) == "" || expire {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && r.Header.Get("X-Csrf-Token") != "csrf/"+strings.TrimPrefix(cookie(r, appSessionCookie), "s") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		name := map[string]string{
			webAPI + "/programs":                                      "web_programs.json",
			webAPI + "/programs/metrics":                              "web_metrics.json",
			webAPI + "/programs/acmecorp/acme":                        "web_acme.json",
			webAPI + "/programs/hooli/hooli":                          "public_hidden.json",
			webAPI + "/statistics/programs/" + acmeID + "/ninetydays": "web_ninetydays.json",
			webAPI + "/programs/" + acmeID + "/update":                "web_updates.json",
			webAPI + "/invites":                                       "web_invites.json",
			webAPI + "/submissions":                                   "web_submissions.json",
			webAPI + "/submissions/ACME-AAAA1111":                     "web_submission_acme1.json",
			webAPI + "/submissions/ACME-BBBB2222":                     "web_submission_acme2.json",
			webAPI + "/submissions/GLOB-CCCC3333":                     "web_submission_glob3.json",
			webAPI + "/program/" + acmeID + "/leaderboard/last90days": "web_leaderboard_90d.json",
			webAPI + "/program/" + acmeID + "/leaderboard/allTime":    "web_leaderboard_all.json",
		}[r.URL.Path]
		// acme's monthly leaderboards: October 2026 as its 90-day board,
		// any other month empty; globex's can't be opened.
		if r.URL.Path == webAPI+"/program/"+acmeID+"/leaderboard/month" && r.URL.Query().Get("year") == "2026" && r.URL.Query().Get("month") == "10" {
			name = "web_leaderboard_90d.json"
		} else if r.URL.Path == webAPI+"/program/"+acmeID+"/leaderboard/month" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if r.URL.Path == webAPI+"/programs/globexco/globex" || r.URL.Path == webAPI+"/program/"+globexID+"/leaderboard/month" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		// The one invitation (web_invites.json) can be accepted.
		if r.URL.Path == webAPI+"/bug-bounty-programs/"+globexID+"/accept-invite" && r.Method == http.MethodPost {
			if b, _ := io.ReadAll(r.Body); string(b) != "{}" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			return
		}
		if name == "" {
			http.NotFound(w, r)
			return
		}
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		if f.noUpdates && name == "web_acme.json" {
			b = bytes.ReplaceAll(b, []byte(`"hasUpdates": true`), []byte(`"hasUpdates": false`))
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(b)
	})
	return mux
}

const (
	acmeID   = "11111111-1111-4111-8111-111111111111"
	globexID = "22222222-2222-4222-8222-222222222222"
	hooliID  = "44444444-4444-4444-8444-444444444444"
)

// webClient returns a client logged in through f; login is what the
// browser login does (here: sets the sign-on cookie, or fails).
func webClient(t *testing.T, f *fakeWeb, login func(c *Client) error) *Client {
	t.Helper()
	f.hits = map[string]int{}
	f.srv = httptest.NewServer(f.handler(t))
	t.Cleanup(f.srv.Close)
	jar, _ := cookiejar.New(nil)
	var c *Client
	c, err := New(Options{Token: token, BaseURL: f.srv.URL + "/api", PublicURL: f.srv.URL + "/public", AppURL: f.srv.URL,
		Email: "hunter@example.com", Password: "pw", Jar: jar,
		HTTP:  httpclient.New(httpclient.Options{RatePerSecond: 1000, MaxAttempts: 1, Jar: jar}),
		Login: func(ctx context.Context) error { return login(c) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *fakeWeb) giveSSO(c *Client) error {
	f.mu.Lock()
	f.ssoValid = true
	f.mu.Unlock()
	u, _ := url.Parse(f.srv.URL)
	c.web.jar.SetCookies(u, []*http.Cookie{{Name: "sso", Value: "1", Path: "/"}})
	return nil
}

func TestWebStats(t *testing.T) {
	f := &fakeWeb{}
	logins := 0
	c := webClient(t, f, func(c *Client) error { logins++; return f.giveSSO(c) })
	got := map[string]platform.ProgramStats{}
	err := c.WebSync(context.Background(), []string{acmeID, globexID, hooliID}, func(_ []string, s map[string]platform.ProgramStats) error {
		for h, st := range s {
			got[h] = st
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	i := func(n int) *int { return &n }
	fl := func(v float64) *float64 { return &v }
	tm := func(sec int64) *time.Time { v := time.Unix(sec, 0).UTC(); return &v }
	want := map[string]platform.ProgramStats{
		acmeID: {Currency: "EUR", BountiesPaid: fl(84052), AvgBountyLow: fl(850), AvgBountyHigh: fl(850), TotalReports: i(742),
			AcceptedReports: i(96), Hunters: i(229), LastSubmissionAt: tm(1793130000), LastAcceptedAt: tm(1793000000),
			// Its leaderboards: 46 of 300 points in 90 days, by 3 researchers
			// (one listed with none).
			Points90d: i(46), PointsAll: i(300), Hunters90d: i(3),
			FirstResponseHours: fl(2), TriageHours: fl(3), ValidationHours: nil, Rewards: acmeRewards,
			Requirements: &platform.Requirements{IDVerified: true, TwoFactor: true, Terms: true},
			// The newest version of each brief section, and the credential pool.
			Profile: platform.ProgramProfile{InScope: "Test only **your own** accounts.", OutScope: "#### Not in scope\nshop.example.com",
				Severity: "Contextual CVSS applies.", FAQ: "### Where are credentials?\nOn request.",
				CredentialPool: &platform.CredentialPool{Available: true, Max: 2}},
			// Newest first; the one without an ID is skipped.
			Announcements: []platform.Announcement{
				{ID: "aaaaaaaa-0000-4000-8000-000000000002", Title: "Bonus month", Body: "Double bounties on Tier 1.", PublishedAt: *tm(1791000000)},
				{ID: "aaaaaaaa-0000-4000-8000-000000000001", Title: "Scope extended", Body: "New API at **api.example.com**.", PublishedAt: *tm(1790000000)},
			}},
		// Must apply first: its page answers 403, so only the metrics.
		globexID: {Currency: "USD", AvgBountyLow: fl(175), AvgBountyHigh: fl(175), AcceptedReports: i(12), LastSubmissionAt: tm(1793000000),
			Requirements: &platform.Requirements{Application: true, EEA: true}},
		// Hidden counts: the page's 0 is not shown; no 90-day times read for it.
		hooliID: {Currency: "USD", BountiesPaid: fl(968898), AvgBountyLow: fl(1449), AvgBountyHigh: fl(1449), AcceptedReports: i(682),
			Requirements: &platform.Requirements{}},
	}
	if !reflect.DeepEqual(got, want) {
		for h := range want {
			if !reflect.DeepEqual(got[h], want[h]) {
				t.Errorf("%s:\n got %+v\nwant %+v", h[:8], got[h], want[h])
			}
		}
	}
	if logins != 1 || f.sessions != 1 {
		t.Errorf("logins %d, app sessions %d; want 1 and 1", logins, f.sessions)
	}

	// A page saying the program has no updates skips asking for them, and
	// counts as read: none.
	f.mu.Lock()
	f.noUpdates = true
	asked := f.hits["GET "+webAPI+"/programs/"+acmeID+"/update"]
	f.mu.Unlock()
	if err := c.WebSync(context.Background(), []string{acmeID}, func(_ []string, s map[string]platform.ProgramStats) error {
		if a := s[acmeID].Announcements; a == nil || len(a) != 0 {
			t.Errorf("announcements without updates = %#v, want read and empty", a)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	if n := f.hits["GET "+webAPI+"/programs/"+acmeID+"/update"]; asked != 1 || n != asked {
		t.Errorf("updates asked %d times, then %d; want once, then not again", asked, n)
	}
	f.mu.Unlock()

	// The listing gains the launch date and the requirements, from the list.
	p := platform.Program{Handle: acmeID}
	c.enrich(&p, levelPublic)
	want2 := platform.Requirements{IDVerified: true, TwoFactor: true, Terms: true}
	if p.StartedAt == nil || !p.StartedAt.Equal(time.Unix(1755717895, 0)) || p.Requirements == nil || *p.Requirements != want2 {
		t.Errorf("enriched acme: %v %+v", p.StartedAt, p.Requirements)
	}
	if p.Collaboration == nil || !*p.Collaboration || p.Industry != "Retail" {
		t.Errorf("enriched acme: collaboration %v, industry %q", p.Collaboration, p.Industry)
	}
	p = platform.Program{Handle: globexID}
	c.enrich(&p, levelApplication)
	if want := (platform.Requirements{Application: true, EEA: true}); p.Requirements == nil || *p.Requirements != want {
		t.Errorf("enriched globex: %+v, want %+v", p.Requirements, want)
	}
	p = platform.Program{Handle: "99999999-9999-4999-8999-999999999999", Requirements: &platform.Requirements{}}
	c.enrich(&p, levelPublic)
	if p.Requirements != nil {
		t.Error("a program the list doesn't have keeps requirements, which would reset the stored ID check")
	}

	// An expired app session is renewed with the sign-on cookie, no login.
	f.mu.Lock()
	f.expire = true
	f.mu.Unlock()
	var list []webProgramJSON
	if err := c.webJSON(context.Background(), http.MethodGet, "/programs", nil, &list); err != nil || len(list) != 3 {
		t.Fatalf("after expiry: %v, %d programs", err, len(list))
	}
	if logins != 1 || f.sessions != 2 {
		t.Errorf("after expiry: logins %d, app sessions %d; want 1 and 2", logins, f.sessions)
	}

	// An expired sign-on cookie means a new login.
	f.mu.Lock()
	f.expire, f.ssoValid = true, false
	f.mu.Unlock()
	if err := c.webJSON(context.Background(), http.MethodGet, "/programs", nil, &list); err != nil {
		t.Fatal(err)
	}
	if logins != 2 || f.sessions != 3 {
		t.Errorf("after the sign-on expired: logins %d, app sessions %d; want 2 and 3", logins, f.sessions)
	}
}

func TestWebLoginFailures(t *testing.T) {
	t.Run("rejected credentials stick", func(t *testing.T) {
		f := &fakeWeb{}
		tries := 0
		c := webClient(t, f, func(*Client) error { tries++; return rejected("Invalid username or password") })
		for range 3 {
			var list []webProgramJSON
			if err := c.webJSON(context.Background(), http.MethodGet, "/programs", nil, &list); err == nil {
				t.Fatal("no error")
			}
		}
		if tries != 1 {
			t.Errorf("tried %d times, want 1", tries)
		}
	})
	t.Run("other failures wait before trying again", func(t *testing.T) {
		f := &fakeWeb{}
		tries := 0
		c := webClient(t, f, func(*Client) error { tries++; return errors.New("intigriti: login: took Chrome for a bot") })
		now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
		c.web.history.Now = func() time.Time { return now }
		var list []webProgramJSON
		_ = c.webJSON(context.Background(), http.MethodGet, "/programs", nil, &list)
		_ = c.webJSON(context.Background(), http.MethodGet, "/programs", nil, &list)
		if ws := c.WebSession(); ws.SignedIn || ws.Stuck || !strings.Contains(ws.Failure, "bot") || ws.RetryAfter == nil || !ws.RetryAfter.Equal(now.Add(weblogin.RetryAfter)) {
			t.Errorf("session after a failure = %+v", ws)
		}
		now = now.Add(weblogin.RetryAfter + time.Minute)
		_ = c.webJSON(context.Background(), http.MethodGet, "/programs", nil, &list)
		if tries != 2 {
			t.Errorf("tried %d times, want 2", tries)
		}
	})
	t.Run("the wait survives a restart", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "sessions", "login.json")
		now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
		var list []webProgramJSON
		c := webClient(t, &fakeWeb{}, func(*Client) error { return errors.New("intigriti: login: took Chrome for a bot") })
		c.web.history = weblogin.NewHistory(state)
		c.web.history.Now = func() time.Time { return now }
		_ = c.webJSON(context.Background(), http.MethodGet, "/programs", nil, &list)
		if fi, err := os.Stat(state); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("state file: %v, %v", fi, err)
		}

		// A new client on the same file, as after a restart.
		f := &fakeWeb{}
		tries := 0
		c2 := webClient(t, f, func(c *Client) error { tries++; return f.giveSSO(c) })
		c2.web.history = weblogin.NewHistory(state)
		c2.web.history.Now = func() time.Time { return now.Add(10 * time.Minute) }
		if err := c2.webJSON(context.Background(), http.MethodGet, "/programs", nil, &list); err == nil || !strings.Contains(err.Error(), "retrying after") {
			t.Errorf("err = %v, want the saved failure", err)
		}
		c2.web.history.Now = func() time.Time { return now.Add(weblogin.RetryAfter + time.Minute) }
		if err := c2.webJSON(context.Background(), http.MethodGet, "/programs", nil, &list); err != nil {
			t.Fatal(err)
		}
		if tries != 1 {
			t.Errorf("tried %d times, want 1", tries)
		}
		if raw, err := os.ReadFile(state); err != nil || strings.Contains(string(raw), `"error"`) {
			t.Errorf("a successful login left the failure behind: %s, %v", raw, err)
		}
		// The status the settings page shows, also after a restart.
		ws := c2.WebSession()
		if !ws.SignedIn || ws.Failure != "" || ws.LastBrowserLogin == nil || !ws.LastBrowserLogin.Equal(now.Add(weblogin.RetryAfter+time.Minute)) {
			t.Errorf("session after the login = %+v", ws)
		}
		c3 := webClient(t, &fakeWeb{}, nil)
		c3.web.history = weblogin.NewHistory(state)
		if ws := c3.WebSession(); ws.LastBrowserLogin == nil || ws.Failure != "" {
			t.Errorf("session after a restart = %+v", ws)
		}
	})
	t.Run("stats fall back to the public pages", func(t *testing.T) {
		f := &fakeWeb{}
		c := webClient(t, f, func(*Client) error { return rejected("Invalid username or password") })
		var got []string
		err := c.WebSync(context.Background(), []string{acmeID}, func(_ []string, s map[string]platform.ProgramStats) error {
			for h := range s {
				got = append(got, h)
			}
			return nil
		})
		// The fake has no public pages: both errors come back.
		if err == nil || !strings.Contains(err.Error(), "Invalid username") || !strings.Contains(err.Error(), "public program list") {
			t.Errorf("err = %v, want the login's and the public pages' errors", err)
		}
		if len(got) != 0 {
			t.Errorf("stats for %v", got)
		}
	})
}

func TestNewWebLogin(t *testing.T) {
	if _, err := New(Options{Token: token, Email: "a@example.com", Password: "pw"}); err == nil {
		t.Error("a web login without a cookie jar")
	}
	jar, _ := cookiejar.New(nil)
	if _, err := New(Options{Token: token, Email: "a@example.com", Jar: jar}); err == nil {
		t.Error("an email without a password")
	}
}
