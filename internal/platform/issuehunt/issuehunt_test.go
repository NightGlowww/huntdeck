package issuehunt

import (
	"context"
	"errors"
	"maps"
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
		opts.Rate = 1000
	}
	c, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.http = httpclient.New(httpclient.Options{
		RatePerSecond: 1000,
		Burst:         100,
		MaxAttempts:   3,
		BaseBackoff:   time.Millisecond,
		RateLimitWait: time.Millisecond,
		Sleep:         func(context.Context, time.Duration) error { return nil },
	})
	return c
}

func TestDefaultsAndCredentials(t *testing.T) {
	tests := []struct {
		name           string
		opts           Options
		wantAuth       bool
		wantCredName   string
		wantReportsCfg bool
		wantBase       string
		wantWeb        string
		wantRate       float64
		wantConc       int
	}{
		{
			name:           "defaults with empty options",
			opts:           Options{},
			wantAuth:       false,
			wantReportsCfg: false,
			wantBase:       "https://api.issuehunt.io",
			wantWeb:        "https://issuehunt.io",
			wantRate:       1.5,
			wantConc:       2,
		},
		{
			name:           "api key sets API key credential",
			opts:           Options{APIKey: "ihk_sample000000000000000000000000000000000000000000000000000000000000"},
			wantAuth:       true,
			wantCredName:   "API key",
			wantReportsCfg: true,
			wantBase:       "https://api.issuehunt.io",
			wantWeb:        "https://issuehunt.io",
		},
		{
			name:           "Bearer prefix is stripped cleanly",
			opts:           Options{APIKey: "Bearer ihk_sample123"},
			wantAuth:       true,
			wantCredName:   "API key",
			wantReportsCfg: true,
			wantBase:       "https://api.issuehunt.io",
			wantWeb:        "https://issuehunt.io",
		},
		{
			name:           "public only ignores APIKey",
			opts:           Options{APIKey: "ihk_sample123", PublicOnly: true},
			wantAuth:       false,
			wantReportsCfg: false,
			wantBase:       "https://api.issuehunt.io",
			wantWeb:        "https://issuehunt.io",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(tc.opts)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if c.Name() != "issuehunt" {
				t.Errorf("Name() = %q, want issuehunt", c.Name())
			}
			if c.base.String() != tc.wantBase {
				t.Errorf("base = %q, want %q", c.base.String(), tc.wantBase)
			}
			if c.web.String() != tc.wantWeb {
				t.Errorf("web = %q, want %q", c.web.String(), tc.wantWeb)
			}
			if tc.wantConc > 0 && c.concurrency != tc.wantConc {
				t.Errorf("concurrency = %d, want %d", c.concurrency, tc.wantConc)
			}
			if c.authenticated() != tc.wantAuth {
				t.Errorf("authenticated() = %v, want %v", c.authenticated(), tc.wantAuth)
			}
			if c.ReportsConfigured() != tc.wantReportsCfg {
				t.Errorf("ReportsConfigured() = %v, want %v", c.ReportsConfigured(), tc.wantReportsCfg)
			}
			creds := c.Credentials()
			if tc.wantAuth {
				if len(creds) != 1 || creds[0].Name != tc.wantCredName {
					t.Errorf("Credentials() = %+v, want [%s]", creds, tc.wantCredName)
				}
				if strings.HasPrefix(c.apiKey, "Bearer ") {
					t.Errorf("apiKey still has Bearer prefix")
				}
			} else {
				if len(creds) != 0 {
					t.Errorf("Credentials() = %+v, want nil", creds)
				}
				if c.Account() != "" {
					t.Errorf("Account() = %q, want empty", c.Account())
				}
			}
		})
	}
}

func TestParseTimeJSDateAndRFC3339(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string // RFC3339 UTC, or "" for nil
	}{
		{
			name: "RFC3339 with millis",
			raw:  "2024-08-09T03:59:43.000Z",
			want: "2024-08-09T03:59:43Z",
		},
		{
			name: "RFC3339 with offset",
			raw:  "2025-05-01T21:00:00+09:00",
			want: "2025-05-01T12:00:00Z",
		},
		{
			name: "JS Date.toString UTC",
			raw:  "Fri Nov 27 2026 10:40:36 GMT+0000 (Coordinated Universal Time)",
			want: "2026-11-27T10:40:36Z",
		},
		{
			name: "JS Date.toString JST",
			raw:  "Thu Feb 13 2025 18:08:29 GMT+0900 (Japan Standard Time)",
			want: "2025-02-13T09:08:29Z",
		},
		{
			name: "empty",
			raw:  "",
			want: "",
		},
		{
			name: "invalid",
			raw:  "not-a-timestamp",
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseTime(tc.raw)
			if tc.want == "" {
				if got != nil {
					t.Fatalf("parseTime(%q) = %v, want nil", tc.raw, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseTime(%q) = nil, want %s", tc.raw, tc.want)
			}
			if got.UTC().Format(time.RFC3339) != tc.want {
				t.Errorf("parseTime(%q) = %s, want %s", tc.raw, got.UTC().Format(time.RFC3339), tc.want)
			}
		})
	}
}

func TestAuthAndPlainTextErrorsAndRetries(t *testing.T) {
	t.Run("plain text 401 marks credential refused and returns ErrAuth", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("User not logged in"))
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, APIKey: "ihk_bad_token"})
		listing, err := c.ListPrograms(context.Background())
		if !errors.Is(err, ErrAuth) {
			t.Fatalf("expected ErrAuth, got %v", err)
		}
		if listing.Complete {
			t.Fatal("expected Complete=false on 401")
		}
		if !strings.Contains(err.Error(), "User not logged in") {
			t.Errorf("expected plain text error body in err, got %v", err)
		}
		creds := c.Credentials()
		if len(creds) != 1 || !creds[0].Stuck || creds[0].Failure == "" {
			t.Errorf("expected stuck refused credential, got %+v", creds)
		}
	})

	t.Run("plain text 403 404 422 do not fail JSON decoding", func(t *testing.T) {
		for _, tc := range []struct {
			code int
			body string
		}{
			{403, "Forbidden"},
			{404, "Program with ID missing-slug does not exist"},
			{422, "Something went wrong with the request."},
		} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			c := newTestClient(t, Options{BaseURL: srv.URL, PublicOnly: true})
			err := c.getJSON(context.Background(), "/programs/missing-slug", nil)
			srv.Close()
			if !isHTTPStatus(err, tc.code) {
				t.Fatalf("status %d: got err=%v", tc.code, err)
			}
			if !strings.Contains(err.Error(), tc.body) {
				t.Errorf("status %d: expected body %q in error %v", tc.code, tc.body, err)
			}
		}
	})

	t.Run("429 and 5xx retry and record Stats", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := calls.Add(1)
			switch n {
			case 1:
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte("Too Many Requests"))
			case 2:
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("Bad Gateway"))
			default:
				_, _ = w.Write(readFixture(t, "user_authenticated.json"))
			}
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, APIKey: "ihk_valid_token"})
		uid, err := c.ensureAuth(context.Background())
		if err != nil {
			t.Fatalf("ensureAuth: %v", err)
		}
		if uid != "00000000-0000-0000-0000-000000000001" {
			t.Errorf("uid = %q", uid)
		}
		reqs, limited := c.Stats()
		if reqs != 3 || limited != 1 {
			t.Errorf("Stats() = (%d, %d), want (3, 1)", reqs, limited)
		}
		if got := c.Account(); got != "sample_user" {
			t.Errorf("Account() = %q, want sample_user", got)
		}
	})
}

func TestListProgramsPaginationCacheAndFields(t *testing.T) {
	var detailCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("visibility") {
			t.Errorf("unexpected visibility query param on %s", r.URL.String())
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte("Something went wrong with the request."))
			return
		}
		if r.Header.Get("Authorization") != "Bearer ihk_sample_key" {
			t.Errorf("missing/wrong Authorization header on %s: %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/users/authenticated":
			_, _ = w.Write(readFixture(t, "user_authenticated.json"))
		case "/programs":
			if r.URL.Query().Get("perPage") != "200" {
				t.Errorf("expected perPage=200, got %q", r.URL.Query().Get("perPage"))
			}
			if r.URL.Query().Get("page") == "2" {
				_, _ = w.Write(readFixture(t, "programs_p2.json"))
			} else {
				_, _ = w.Write(readFixture(t, "programs_p1.json"))
			}
		case "/programs/acme-bounty":
			detailCalls.Add(1)
			_, _ = w.Write(readFixture(t, "program_detail.json"))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, Options{BaseURL: srv.URL, WebURL: "https://issuehunt.io", APIKey: "ihk_sample_key"})
	listing, err := c.ListPrograms(context.Background())
	if err != nil {
		t.Fatalf("ListPrograms: %v", err)
	}
	if !listing.Complete {
		t.Fatal("expected Complete=true")
	}
	if len(listing.Programs) != 4 {
		t.Fatalf("got %d programs, want 4", len(listing.Programs))
	}
	if c.Account() != "sample_user" {
		t.Errorf("Account() = %q, want sample_user", c.Account())
	}

	byHandle := map[string]platform.Program{}
	for _, p := range listing.Programs {
		byHandle[p.Handle] = p
	}

	// acme-bounty: public, open, JPY, rewards 10000..300000, en policy preferred over jp
	acme := byHandle["acme-bounty"]
	if acme.PlatformID != "00000000-0000-0000-0000-000000000101" || acme.Private || acme.State != platform.StateOpen {
		t.Errorf("acme identity/state = %+v", acme)
	}
	if !acme.OffersBounty || acme.Currency != "JPY" || acme.BountyMin == nil || *acme.BountyMin != 10000 || acme.BountyMax == nil || *acme.BountyMax != 300000 {
		t.Errorf("acme bounty = offers:%v cur:%q min:%v max:%v", acme.OffersBounty, acme.Currency, acme.BountyMin, acme.BountyMax)
	}
	if !strings.Contains(acme.Policy, "## Program Terms") || strings.Contains(acme.Policy, "プログラム規約") {
		t.Errorf("acme policy should prefer en over jp, got %q", acme.Policy)
	}
	if acme.StartedAt == nil || acme.StartedAt.UTC().Format(time.RFC3339) != "2025-02-13T09:08:29Z" {
		t.Errorf("acme StartedAt = %v", acme.StartedAt)
	}
	if acme.Requirements == nil || acme.Requirements.Application {
		t.Errorf("acme Requirements = %+v", acme.Requirements)
	}

	// beta-vdp: frozen -> StatePaused, vdp -> OffersBounty=false, jp fallback policy
	beta := byHandle["beta-vdp"]
	if beta.State != platform.StatePaused || beta.OffersBounty || beta.Currency != "" {
		t.Errorf("beta = state:%q offers:%v cur:%q", beta.State, beta.OffersBounty, beta.Currency)
	}
	if !strings.Contains(beta.Policy, "脆弱性開示ポリシー") {
		t.Errorf("beta should fall back to jp policy, got %q", beta.Policy)
	}

	// gamma-app-only: visibility=application -> Requirements.Application=true, Private=false
	gamma := byHandle["gamma-app-only"]
	if gamma.Private || gamma.Requirements == nil || !gamma.Requirements.Application {
		t.Errorf("gamma = private:%v req:%+v", gamma.Private, gamma.Requirements)
	}

	// delta-internal-points: activated=false -> StateClosed, invite -> Private=true, point -> OffersBounty=false
	delta := byHandle["delta-internal-points"]
	if !delta.Private || delta.State != platform.StateClosed || delta.OffersBounty || delta.Currency != "" {
		t.Errorf("delta = private:%v state:%q offers:%v cur:%q", delta.Private, delta.State, delta.OffersBounty, delta.Currency)
	}

	// FetchScopeDetail for cached acme-bounty must make 0 extra HTTP calls.
	entries, detail, err := c.FetchScopeDetail(context.Background(), acme)
	if err != nil {
		t.Fatalf("FetchScopeDetail(acme): %v", err)
	}
	if detailCalls.Load() != 0 {
		t.Errorf("expected cached FetchScopeDetail to make 0 HTTP calls, got %d", detailCalls.Load())
	}
	// Comma-split "*.example.com, api.example.com" + "com.example.ios" -> 3 entries, all InScope: true.
	if len(entries) != 3 {
		t.Fatalf("got %d scope entries, want 3: %+v", len(entries), entries)
	}
	for _, e := range entries {
		if !e.InScope {
			t.Errorf("expected InScope=true on %+v", e)
		}
	}
	if entries[0].Identifier != "*.example.com" || entries[0].AssetType != scope.AssetWildcard {
		t.Errorf("entry[0] = %+v", entries[0])
	}
	if entries[1].Identifier != "api.example.com" || entries[1].AssetType != scope.AssetDomain {
		t.Errorf("entry[1] = %+v", entries[1])
	}
	if entries[0].PlatformID != "" || entries[1].PlatformID != "" {
		t.Errorf("split group entries must not share PlatformID, got %q / %q", entries[0].PlatformID, entries[1].PlatformID)
	}
	if entries[2].Identifier != "com.example.ios" || entries[2].AssetType != scope.AssetIOS || entries[2].MaxSeverity != platform.SeverityHigh {
		t.Errorf("entry[2] = %+v", entries[2])
	}
	if len(detail.Rewards) != 2 {
		t.Fatalf("got %d reward rows, want 2 (Default + VRT): %+v", len(detail.Rewards), detail.Rewards)
	}
	if detail.Rewards[0].Low.Min != 0 || detail.Rewards[0].Low.Max != 10000 || detail.Rewards[0].Critical.Min != 100000 || detail.Rewards[0].Critical.Max != 300000 {
		t.Errorf("unexpected default reward row: %+v", detail.Rewards[0])
	}
	if detail.Rewards[1].Scope != "server_security_misconfiguration" || detail.Rewards[1].High.Min != 50000 || detail.Rewards[1].High.Max != 100000 {
		t.Errorf("unexpected VRT reward row: %+v", detail.Rewards[1])
	}

	// gamma-app-only has visibility=application and empty scopeGroups & contents -> ErrScopeUnavailable
	if _, _, err := c.FetchScopeDetail(context.Background(), gamma); !errors.Is(err, platform.ErrScopeUnavailable) {
		t.Fatalf("expected ErrScopeUnavailable for unapproved application program, got %v", err)
	}
}

func TestListProgramsPublicOnlyAndPartialFailure(t *testing.T) {
	t.Run("public only omits Authorization header", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "" {
				t.Errorf("unexpected Authorization header in PublicOnly mode: %q", r.Header.Get("Authorization"))
			}
			if r.URL.Path != "/programs" {
				t.Errorf("unexpected path %s in PublicOnly mode", r.URL.Path)
			}
			if r.URL.Query().Get("page") == "2" {
				_, _ = w.Write(readFixture(t, "programs_p2.json"))
			} else {
				_, _ = w.Write(readFixture(t, "programs_p1.json"))
			}
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, PublicOnly: true, APIKey: "should-not-be-sent"})
		listing, err := c.ListPrograms(context.Background())
		if err != nil || !listing.Complete || len(listing.Programs) != 4 {
			t.Fatalf("PublicOnly ListPrograms = len:%d complete:%v err:%v", len(listing.Programs), listing.Complete, err)
		}
	})

	t.Run("page 2 failure returns partial results with Complete=false", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("page") == "2" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("Invalid page"))
				return
			}
			_, _ = w.Write(readFixture(t, "programs_p1.json"))
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, PublicOnly: true})
		listing, err := c.ListPrograms(context.Background())
		if err == nil {
			t.Fatal("expected error on page 2 failure")
		}
		if listing.Complete {
			t.Fatal("expected Complete=false")
		}
		if len(listing.Programs) != 2 {
			t.Fatalf("expected 2 partial programs from page 1, got %d", len(listing.Programs))
		}
	})

	t.Run("cache miss falls back to GET /programs/{slugOrId}", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/programs/acme-bounty" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(readFixture(t, "program_detail.json"))
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, PublicOnly: true})
		entries, err := c.FetchScope(context.Background(), platform.Program{Handle: "acme-bounty"})
		if err != nil {
			t.Fatalf("FetchScope fallback: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(entries))
		}
	})
}

func TestFetchReportsAndActivitiesNeverCallsMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "messages") {
			t.Fatalf("FORBIDDEN: FetchReports called %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/users/authenticated":
			_, _ = w.Write(readFixture(t, "user_authenticated.json"))
		case "/users/00000000-0000-0000-0000-000000000001/activities":
			_, _ = w.Write(readFixture(t, "user_activities.json"))
		case "/reports":
			if r.URL.Query().Get("perPage") != "200" {
				t.Errorf("expected perPage=200, got %q", r.URL.Query().Get("perPage"))
			}
			_, _ = w.Write(readFixture(t, "reports_p1.json"))
		default:
			t.Errorf("unexpected request %s", r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, Options{BaseURL: srv.URL, APIKey: "ihk_sample_key"})
	var got []platform.Report
	complete, err := c.FetchReports(context.Background(), nil, 100, func(_ context.Context, rs []platform.Report) error {
		got = append(got, rs...)
		return nil
	})
	if err != nil {
		t.Fatalf("FetchReports: %v", err)
	}
	if !complete {
		t.Fatal("expected complete=true")
	}
	if len(got) != 3 {
		t.Fatalf("got %d reports, want 3", len(got))
	}

	// Report 901: resolved, 2 rewarded money events (100000 + 50000), severity changed medium -> high
	r1 := got[0]
	if r1.State != platform.ReportResolved || r1.Severity != platform.SeverityHigh || !r1.SeverityChanged {
		t.Errorf("r1 = %+v", r1)
	}
	if !r1.PaymentsKnown || len(r1.Payments) != 2 {
		t.Fatalf("r1 payments = known:%v %+v", r1.PaymentsKnown, r1.Payments)
	}
	var sum float64
	for _, p := range r1.Payments {
		if p.Currency != "JPY" {
			t.Errorf("payment currency = %q, want JPY", p.Currency)
		}
		sum += p.Amount
	}
	if sum != 150000 {
		t.Errorf("r1 bounty sum = %v, want 150000", sum)
	}
	if r1.Comments != 3 || r1.Unread {
		t.Errorf("r1 comments=%d unread=%v", r1.Comments, r1.Unread)
	}

	// Report 902: upstream typo status + JS Date.toString() sentAt + unread=true
	r2 := got[1]
	if r2.State != platform.ReportNotApplicable || !r2.Unread {
		t.Errorf("r2 state=%q unread=%v", r2.State, r2.Unread)
	}
	if r2.SubmittedAt.UTC().Format(time.RFC3339) != "2026-11-27T10:40:36Z" {
		t.Errorf("r2 SubmittedAt = %v", r2.SubmittedAt)
	}

	// Report 903: "not_reproducible" + point system reward ignored + CVSS score
	r3 := got[2]
	if r3.State != platform.ReportNotApplicable || len(r3.Payments) != 0 {
		t.Errorf("r3 state=%q payments=%+v", r3.State, r3.Payments)
	}
	if r3.CVSS == nil || *r3.CVSS != 3.5 {
		t.Errorf("r3 CVSS = %v, want 3.5", r3.CVSS)
	}
}

func TestFetchDisclosuresCountPaginationAndNilFeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/programs/00000000-0000-0000-0000-000000000101/disclosures":
			if r.URL.Query().Get("page") == "2" {
				_, _ = w.Write(readFixture(t, "disclosures_p2.json"))
			} else {
				_, _ = w.Write(readFixture(t, "disclosures_p1.json"))
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, Options{BaseURL: srv.URL, PublicOnly: true})

	// p == nil (all-programs feed) returns nil cleanly without hitting server.
	if err := c.FetchDisclosures(context.Background(), nil, 5, func([]platform.Disclosure) (bool, error) {
		t.Fatal("save should not be called when p is nil")
		return false, nil
	}); err != nil {
		t.Fatalf("FetchDisclosures(nil): %v", err)
	}

	var all []platform.Disclosure
	prog := &platform.Program{
		Handle:     "acme-bounty",
		PlatformID: "00000000-0000-0000-0000-000000000101",
	}
	err := c.FetchDisclosures(context.Background(), prog, 10, func(page []platform.Disclosure) (bool, error) {
		all = append(all, page...)
		return true, nil
	})
	if err != nil {
		t.Fatalf("FetchDisclosures(acme): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d disclosures across 2 count-paginated pages, want 2", len(all))
	}
	if all[0].ID != "00000000-0000-0000-0000-000000000901" || all[0].Severity != platform.SeverityHigh || all[0].Bounty == nil || *all[0].Bounty != 100000 {
		t.Errorf("disclosure[0] = %+v", all[0])
	}
	if all[0].Summary != "Vendor fixed input sanitization" || all[0].Reporter != "Sample Researcher" {
		t.Errorf("disclosure[0] summary/reporter = %q / %q", all[0].Summary, all[0].Reporter)
	}
	if all[1].DisclosedAt.UTC().Format(time.RFC3339) != "2025-02-13T09:08:29Z" || all[1].Summary != "SSRF summary" {
		t.Errorf("disclosure[1] = %+v", all[1])
	}
}

func TestFetchProgramStats(t *testing.T) {
	var orgCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/programs/acme-bounty":
			_, _ = w.Write(readFixture(t, "program_detail.json"))
		case "/programs/gone-program":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("Program with ID gone-program does not exist"))
		case "/programs/00000000-0000-0000-0000-000000000101/hof":
			_, _ = w.Write(readFixture(t, "hof.json"))
		case "/organizations/00000000-0000-0000-0000-000000000301":
			orgCalls.Add(1)
			_, _ = w.Write(readFixture(t, "organization.json"))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, Options{BaseURL: srv.URL, PublicOnly: true})
	got := map[string]platform.ProgramStats{}
	err := c.FetchProgramStats(context.Background(), []string{"acme-bounty", "gone-program"}, func(_ []string, stats map[string]platform.ProgramStats) error {
		maps.Copy(got, stats)
		return nil
	})
	if err != nil {
		t.Fatalf("FetchProgramStats: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d stats, want 1", len(got))
	}
	st := got["acme-bounty"]
	if st.Currency != "JPY" {
		t.Errorf("Currency = %q, want JPY", st.Currency)
	}
	if st.FirstResponseHours == nil || *st.FirstResponseHours != 120 {
		t.Errorf("FirstResponseHours = %v, want 120 (432000/3600)", st.FirstResponseHours)
	}
	if st.HackersThanked == nil || *st.HackersThanked != 3 {
		t.Errorf("HackersThanked = %v, want 3", st.HackersThanked)
	}
	if st.Profile.Introduction != "Acme Corp builds sample cloud software." {
		t.Errorf("Profile.Introduction = %q", st.Profile.Introduction)
	}
	if orgCalls.Load() != 1 {
		t.Errorf("orgCalls = %d, want 1", orgCalls.Load())
	}
}
