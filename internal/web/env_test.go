package web

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/logging"
	"github.com/mdpsec/huntdeck/internal/notify"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/fake"
	"github.com/mdpsec/huntdeck/internal/scheduler"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
	"github.com/mdpsec/huntdeck/internal/syncer"
)

type pinger struct{ err error }

func (p pinger) Ping(context.Context) error { return p.err }

type fakePoller struct {
	calls []string
	err   error
	// accepted are the invitations AcceptInvite was asked to accept, as
	// "platform/handle"; it returns acceptErr.
	accepted  []string
	acceptErr error
}

func (f *fakePoller) PollNow(p, mode string) error {
	f.calls = append(f.calls, p+"/"+mode)
	return f.err
}

func (f *fakePoller) AcceptInvite(p, handle string) error {
	f.accepted = append(f.accepted, p+"/"+handle)
	return f.acceptErr
}

type testEnv struct {
	t     *testing.T
	srv   *httptest.Server
	store *store.Store
	// db is the same database the server reads, so a test can make a read
	// fail on purpose: a dropped table is the honest way to ask what a page
	// does when something is unreadable.
	db     *db.DB
	poller *fakePoller
	sched  *scheduler.Schedule
	fake   *fake.Platform
	syncer *syncer.Syncer
}

// newTestEnv builds a server over a real database seeded through the syncer:
// two programs on the "fake" platform, then a second poll that records
// changes. with changes its dependencies.
func newTestEnv(t *testing.T, with ...func(*Deps)) *testEnv {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	migs, _ := db.Migrations()
	log := slog.New(slog.DiscardHandler)
	if _, err := d.Migrate(ctx, migs, t.TempDir(), log); err != nil {
		t.Fatal(err)
	}
	st := store.New(d, nil)

	f := fake.New()
	acme := platform.Program{Handle: "acme", Name: "Acme Corp", FastPayments: true,
		Policy: "## Program rules\n\n* Use your `@wearehackerone.com` alias\n* See [docs](https://docs.example.com)\n\n<img src=x onerror=alert(1)>", URL: "https://platform.example/acme", State: platform.StateOpen, OffersBounty: true,
		UserStats: &platform.UserStats{Reports: 4, ValidReports: 2, BountyEarned: 1500}}
	evil := platform.Program{Handle: "evil", Name: `<script>alert(1)</script>`, URL: "https://platform.example/evil", Private: true, State: platform.StateOpen,
		Requirements: &platform.Requirements{Application: true}}
	f.SetPrograms(acme, evil)
	f.SetScope("acme",
		platform.ScopeEntry{PlatformID: "1", InScope: true, AssetType: scope.AssetWildcard, Identifier: "*.acme.example", BountyEligible: true, MaxSeverity: "critical"},
		platform.ScopeEntry{PlatformID: "2", InScope: false, AssetType: scope.AssetURL, Identifier: "admin.acme.example"})
	f.SetScope("evil", platform.ScopeEntry{PlatformID: "3", InScope: true, AssetType: scope.AssetURL, Identifier: "app.evil.example",
		Instruction: `<img src=x onerror=alert(1)>`},
		platform.ScopeEntry{PlatformID: "5", InScope: true, AssetType: scope.AssetCIDR, Identifier: "192.0.2.0/24"})
	paid90, paid, avgLo, avgHi, topLo, topHi := 41500.0, 1250000.0, 500.0, 2000.0, 5000.0, 15000.0
	reports90, resolved, efficiency := 340, 120, 96
	f.SetStats("acme", platform.ProgramStats{Currency: "USD", BountiesPaidLast90d: &paid90, ReportsLast90d: &reports90,
		BountiesPaid: &paid, AvgBountyLow: &avgLo, AvgBountyHigh: &avgHi, TopBountyLow: &topLo, TopBountyHigh: &topHi,
		ResolvedReports: &resolved, ResponseEfficiency: &efficiency, KnownIssues: ptrTo(1234), KnownIssuesUnique: ptrTo(987),
		SignalRequirement: ptrTo(3.0), Requirements: &platform.Requirements{MinSignal: ptrTo(3.0), IDVerified: true},
		FirstResponseHours: ptrTo(5.0), TriageHours: ptrTo(66.0), ResolutionHours: ptrTo(1062.0),
		BySeverity: platform.SeverityStats{
			Low:      platform.SeverityStat{AvgBounty: ptrTo(200.0), Reports: ptrTo(6)},
			Medium:   platform.SeverityStat{AvgBounty: ptrTo(521.0), Reports: ptrTo(43)},
			High:     platform.SeverityStat{Reports: ptrTo(34)},
			Critical: platform.SeverityStat{Reports: ptrTo(17)},
		},
		Profile: platform.ProgramProfile{
			Website:          "https://www.acme.example/about",
			CredentialScopes: []string{"1"},
			Introduction:     "Acme takes **security** seriously. <script>alert(1)</script>",
			Campaign:         "# Double bounties\n\nOn [api](javascript:alert(1)) this month.",
			StandardsKnown:   true,
		},
		Rewards: []platform.RewardRow{
			{Scope: "*.acme.example", Low: platform.RewardRange{Max: 200}, Critical: platform.RewardRange{Max: 5000}},
			{Scope: "api.acme.example", Low: platform.RewardRange{Min: 100, Max: 400}, Critical: platform.RewardRange{Min: 10000, Max: 15000}},
		}})
	f.SetStats("evil", platform.ProgramStats{Currency: "USD", ResolvedReports: &resolved,
		Rewards: []platform.RewardRow{{Scope: `<img src=x onerror=alert(1)>`, High: platform.RewardRange{Max: 100}},
			{Scope: "other", High: platform.RewardRange{Max: 50}}}})
	disclosedAt := time.Date(2026, 9, 2, 0, 3, 45, 0, time.UTC)
	f.SetDisclosures(
		platform.Disclosure{ID: "101", ProgramHandle: "acme", Title: `Stored XSS via <script>alert(1)</script> in name`, Severity: "high",
			Weakness: "Cross-site Scripting (XSS) - Stored", Bounty: ptrTo(1337.0), DisclosedAt: disclosedAt, Votes: 36,
			URL: "https://platform.example/reports/101", Reporter: "hunter-example", Summary: "Script runs in the admin panel."},
		platform.Disclosure{ID: "102", ProgramHandle: "acme", Title: "Open redirect", Severity: "low", Weakness: "Open Redirect",
			DisclosedAt: disclosedAt.Add(-48 * time.Hour), URL: "https://platform.example/reports/102"})
	sy := syncer.New(st, log, 2)
	if _, err := sy.Poll(ctx, f, syncer.Options{Mode: syncer.ModeFull, Trigger: "manual"}); err != nil {
		t.Fatal(err)
	}
	f.SetScope("acme",
		platform.ScopeEntry{PlatformID: "1", InScope: true, AssetType: scope.AssetWildcard, Identifier: "*.acme.example", BountyEligible: true, MaxSeverity: "critical"},
		platform.ScopeEntry{PlatformID: "2", InScope: false, AssetType: scope.AssetURL, Identifier: "admin.acme.example"},
		platform.ScopeEntry{PlatformID: "4", InScope: true, AssetType: scope.AssetURL, Identifier: "https://api.acme.example/v2", BountyEligible: true})
	if _, err := sy.Poll(ctx, f, syncer.Options{Mode: syncer.ModeFull, Trigger: "manual"}); err != nil {
		t.Fatal(err)
	}

	p := &fakePoller{}
	sched, err := scheduler.NewSchedule(ctx, st, map[string]scheduler.Settings{
		"fake": {Enabled: true, Quick: 15 * time.Minute, Full: 6 * time.Hour}})
	if err != nil {
		t.Fatal(err)
	}
	deps := Deps{Log: log, DB: pinger{}, Store: st, Poller: p, Version: "test", Schedule: sched, Notify: notify.New(st, log, ""),
		Platforms: []PlatformInfo{{Name: "fake", AcceptsInvites: true, Discloses: true, Credentials: func() []platform.Credential {
			return []platform.Credential{{Name: "API token"}} // not used since the start: the last poll vouches for it
		}}},
		WebSyncs:     map[string]platform.WebSyncer{"fake": fake.Web{Platform: f}},
		Logs:         logging.NewBuffer(500),
		AllowedHosts: []string{"huntdeck.tailnet.example"}, SaveOwn: sy.SaveOwn}
	for _, w := range with {
		w(&deps)
	}
	h, err := NewHandler(deps)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &testEnv{t: t, srv: srv, store: st, db: d, poller: p, sched: sched, fake: f, syncer: sy}
}

// client returns an HTTP client that doesn't follow redirects.
func (e *testEnv) client() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (e *testEnv) do(c *http.Client, method, path string, form url.Values, header http.Header) (*http.Response, string) {
	e.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(context.Background(), method, e.srv.URL+path, body)
	if err != nil {
		e.t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range header {
		if k == "Host" {
			req.Host = v[0]
			continue
		}
		req.Header[k] = v
	}
	res, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func bigList(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "h%d.example.com\n", i)
	}
	return b.String()
}
