package hackerone

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

const wantAuth = "Basic " + "dXNlcjp0b2tlbg==" // user:token

// fakeAPI serves fixtures and lets tests override individual paths.
type fakeAPI struct {
	t        *testing.T
	srv      *httptest.Server
	override map[string]http.HandlerFunc // keyed by "path?query"
	hits     map[string]int
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t, override: map[string]http.HandlerFunc{}, hits: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != wantAuth {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	key := r.URL.Path + "?" + r.URL.RawQuery
	f.hits[r.URL.Path]++
	if h, ok := f.override[key]; ok {
		h(w, r)
		return
	}
	if h, ok := f.override[r.URL.Path]; ok {
		h(w, r)
		return
	}
	switch {
	case r.URL.Path == "/hackers/programs" && r.URL.Query().Get("page[number]") == "2":
		f.fixture(w, "programs_page2.json")
	case r.URL.Path == "/hackers/programs":
		f.fixture(w, "programs_page1.json")
	case r.URL.Path == "/hackers/programs/acme":
		f.fixture(w, "program_acme.json")
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeAPI) fixture(w http.ResponseWriter, name string) {
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		f.t.Fatal(err)
	}
	_, _ = w.Write([]byte(strings.ReplaceAll(string(b), "{{BASE}}", f.srv.URL)))
}

func (f *fakeAPI) client(t *testing.T, user, token string) *Client {
	t.Helper()
	c, err := New(Options{
		Username: user, Token: token, BaseURL: f.srv.URL, ScopesPerMinute: 1e6,
		HTTP: httpclient.New(httpclient.Options{
			RatePerSecond: 1000, Burst: 100, MaxAttempts: 2,
			Sleep: func(context.Context, time.Duration) error { return nil },
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAuthHeader(t *testing.T) {
	if got := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:token")); got != wantAuth {
		t.Fatalf("test constant wrong: %s", got)
	}
}

func TestListPrograms(t *testing.T) {
	f := newFakeAPI(t)
	c := f.client(t, "user", "token")
	l, err := c.ListPrograms(context.Background())
	if err != nil || !l.Complete {
		t.Fatalf("ListPrograms: complete=%v err=%v", l.Complete, err)
	}
	if cr := c.Credentials(); len(cr) != 1 || cr[0].Name != "API token" || cr[0].LastOK == nil || cr[0].Failure != "" {
		t.Errorf("credentials after a good listing = %+v", cr)
	}
	if len(l.Programs) != 3 {
		t.Fatalf("got %d programs, want 3 across two pages", len(l.Programs))
	}

	acme, globex, initech := l.Programs[0], l.Programs[1], l.Programs[2]
	if acme.Handle != "acme" || acme.PlatformID != "1001" || acme.Private || acme.State != platform.StateOpen ||
		!acme.OffersBounty || acme.Currency != "USD" || !acme.FastPayments || !acme.SafeHarbor || !acme.TriageActive ||
		acme.URL != "https://hackerone.com/acme" || acme.Policy != "Be nice." {
		t.Errorf("acme mapped wrong: %+v", acme)
	}
	if acme.StartedAt == nil || !acme.StartedAt.Equal(time.Date(2021, 3, 4, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("acme StartedAt = %v", acme.StartedAt)
	}
	if acme.UserStats == nil || *acme.UserStats != (platform.UserStats{Reports: 3, ValidReports: 1, BountyEarned: 500}) {
		t.Errorf("acme UserStats = %+v", acme.UserStats)
	}
	if acme.BountySplitting == nil || !*acme.BountySplitting {
		t.Errorf("acme BountySplitting = %v, want true", acme.BountySplitting)
	}
	if len(acme.Raw) == 0 || !json.Valid(acme.Raw) {
		t.Error("acme Raw should hold the program JSON")
	}

	if !globex.Private || globex.State != platform.StatePaused || globex.JoinedAt == nil || globex.OpenScope {
		t.Errorf("globex mapped wrong: %+v", globex)
	}
	if initech.State != platform.StateClosed || initech.OffersBounty || initech.UserStats != nil || initech.StartedAt != nil {
		t.Errorf("initech mapped wrong: %+v", initech)
	}
}

func TestListProgramsAuthFailure(t *testing.T) {
	f := newFakeAPI(t)
	c := f.client(t, "user", "wrong")
	l, err := c.ListPrograms(context.Background())
	if !errors.Is(err, ErrAuth) || l.Complete || len(l.Programs) != 0 {
		t.Errorf("got complete=%v programs=%d err=%v; want ErrAuth", l.Complete, len(l.Programs), err)
	}
	if cr := c.Credentials(); len(cr) != 1 || cr[0].LastFail == nil || !strings.Contains(cr[0].Failure, "401") || strings.Contains(cr[0].Failure, "wrong") {
		t.Errorf("credentials after a refusal = %+v", cr)
	}
}

func TestListProgramsPartialFailure(t *testing.T) {
	f := newFakeAPI(t)
	f.override["/hackers/programs?page%5Bnumber%5D=2&page%5Bsize%5D=100"] = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}
	l, err := f.client(t, "user", "token").ListPrograms(context.Background())
	if err == nil || l.Complete || len(l.Programs) != 2 {
		t.Errorf("got complete=%v programs=%d err=%v; want first page only, incomplete, with error", l.Complete, len(l.Programs), err)
	}
}

func TestListProgramsRejectsForeignNextLink(t *testing.T) {
	f := newFakeAPI(t)
	f.override["/hackers/programs"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[],"links":{"next":"https://evil.example/steal"}}`))
	}
	_, err := f.client(t, "user", "token").ListPrograms(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unexpected host") {
		t.Errorf("err = %v; want refusal to follow foreign link", err)
	}
}

func TestFetchScopeInline(t *testing.T) {
	f := newFakeAPI(t)
	entries, err := f.client(t, "user", "token").FetchScope(context.Background(), platform.Program{Handle: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	want := []struct {
		id, assetType, ident string
		in, bounty           bool
	}{
		{"5001", scope.AssetWildcard, "*.acme.example", true, true},
		{"5002", scope.AssetURL, "legacy.acme.example", false, false},
		{"5003", scope.AssetAndroid, "com.acme.example.app", true, true},
	}
	for i, w := range want {
		e := entries[i]
		if e.PlatformID != w.id || e.AssetType != w.assetType || e.Identifier != w.ident || e.InScope != w.in || e.BountyEligible != w.bounty {
			t.Errorf("entry %d = %+v, want %+v", i, e, w)
		}
	}
	if entries[0].MaxSeverity != "critical" || entries[0].Instruction != "All subdomains." || entries[0].UpdatedAt == nil {
		t.Errorf("entry 0 details wrong: %+v", entries[0])
	}
	if c := entries[0].CVSS; c != (platform.CVSSRequirements{Confidentiality: "high", Integrity: "not_defined"}) {
		t.Errorf("entry 0 CVSS requirements = %+v", c)
	}
	if c := entries[1].CVSS; c != (platform.CVSSRequirements{}) {
		t.Errorf("entry 1 CVSS requirements = %+v, want none", c)
	}
	if f.hits["/hackers/programs/acme/structured_scopes"] != 0 {
		t.Error("small inline list should not hit the rate-limited structured_scopes endpoint")
	}
}

func scopeItems(from, to int) string {
	var items []string
	for i := from; i < to; i++ {
		items = append(items, fmt.Sprintf(`{"id":"%d","type":"structured-scope","attributes":{"asset_type":"URL","asset_identifier":"h%d.big.example","eligible_for_bounty":true,"eligible_for_submission":true}}`, i, i))
	}
	return strings.Join(items, ",")
}

func TestFetchScopeFallsBackWhenInlineIsTruncated(t *testing.T) {
	f := newFakeAPI(t)
	const total = 1050
	f.override["/hackers/programs/big"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"relationships":{"structured_scopes":{"data":[%s]}}}`, scopeItems(0, inlineCap))
	}
	var cursors []string
	f.override["/hackers/programs/big/structured_scopes"] = func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("page[number]") != "" || q.Get("page[size]") != "100" {
			t.Errorf("unexpected query %s", r.URL.RawQuery)
		}
		gt, _ := strconv.Atoi(q.Get("filter[id__gt]"))
		cursors = append(cursors, q.Get("filter[id__gt]"))
		from := gt + 1
		if gt == 0 {
			from = 0
		}
		_, _ = fmt.Fprintf(w, `{"data":[%s],"links":{}}`, scopeItems(from, min(from+100, total)))
	}
	entries, err := f.client(t, "user", "token").FetchScope(context.Background(), platform.Program{Handle: "big"})
	if err != nil || len(entries) != total {
		t.Fatalf("got %d entries, err=%v; want %d from the cursor-paginated endpoint", len(entries), err, total)
	}
	if len(cursors) != 11 || cursors[0] != "0" || cursors[1] != "99" {
		t.Errorf("cursors = %v", cursors)
	}
}

func TestFetchScopeInlineBelowCapIsTrusted(t *testing.T) {
	f := newFakeAPI(t)
	f.override["/hackers/programs/mid"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"relationships":{"structured_scopes":{"data":[%s]}}}`, scopeItems(0, 645))
	}
	entries, err := f.client(t, "user", "token").FetchScope(context.Background(), platform.Program{Handle: "mid"})
	if err != nil || len(entries) != 645 || f.hits["/hackers/programs/mid/structured_scopes"] != 0 {
		t.Errorf("entries=%d err=%v paginated hits=%d", len(entries), err, f.hits["/hackers/programs/mid/structured_scopes"])
	}
}

func TestFetchScopeErrors(t *testing.T) {
	f := newFakeAPI(t)
	f.override["/hackers/programs/noscopes"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"1","attributes":{}}`))
	}
	c := f.client(t, "user", "token")
	for _, handle := range []string{"noscopes", "missing"} {
		if _, err := c.FetchScope(context.Background(), platform.Program{Handle: handle}); err == nil {
			t.Errorf("FetchScope(%s): want error", handle)
		}
	}
}

func TestNewRequiresCredentials(t *testing.T) {
	if _, err := New(Options{Username: "u"}); err == nil {
		t.Error("New without token should fail")
	}
}
