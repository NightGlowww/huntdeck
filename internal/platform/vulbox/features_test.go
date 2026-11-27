package vulbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

func TestFetchScopeDetailHTMLAndStructuredAssets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/web/account/basic-info":
			_, _ = w.Write(readFixture(t, "basic_info_ok.json"))
		case "/hacker/project/detail":
			_, _ = w.Write(readFixture(t, "detail_joined.json"))
		case "/hacker/project/detail/assets":
			_, _ = w.Write(readFixture(t, "detail_assets.json"))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, Options{BaseURL: srv.URL, Session: "sample-token"})
	entries, detail, err := c.FetchScopeDetail(context.Background(), platform.Program{Handle: "10001"})
	if err != nil {
		t.Fatalf("FetchScopeDetail: %v", err)
	}

	// Verify merged & deduplicated entries (structured assets + HTML task_range).
	type key struct {
		inScope bool
		typ     string
		ident   string
	}
	got := map[key]bool{}
	for _, e := range entries {
		got[key{e.InScope, e.AssetType, e.Identifier}] = true
	}

	want := []key{
		{true, scope.AssetWildcard, "*.example.com"},
		{true, scope.AssetAndroid, "com.example.acme"},
		{true, scope.AssetDomain, "api.example.org"},
		{true, scope.AssetIP, "203.0.113.10"},
		{true, scope.AssetCIDR, "198.51.100.0/24"},
		{true, scope.AssetURL, "https://portal.example.net/v1/auth"},
		{false, scope.AssetDomain, "test.example.com"},
		{false, scope.AssetDomain, "blog.example.com"},
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing expected scope entry %+v (got %+v)", w, entries)
		}
	}

	// Verify *.example.com was deduplicated (not emitted twice).
	wildCount := 0
	for _, e := range entries {
		if e.Identifier == "*.example.com" && e.InScope {
			wildCount++
		}
	}
	if wildCount != 1 {
		t.Errorf("expected *.example.com once, got %d", wildCount)
	}

	// Verify Policy, Rewards (CNY), Requirements, State, Facts.
	if !strings.Contains(detail.Policy, "Acme Corp official security response center") ||
		!strings.Contains(detail.Policy, "No DoS or social engineering") {
		t.Errorf("unexpected Policy: %q", detail.Policy)
	}
	if len(detail.Rewards) != 1 {
		t.Fatalf("expected 1 reward row, got %d", len(detail.Rewards))
	}
	row := detail.Rewards[0]
	if row.Scope != "核心业务" || row.Low.Min != 100 || row.Low.Max != 500 || row.Critical.Min != 5000 || row.Critical.Max != 10000 {
		t.Errorf("unexpected reward row: %+v", row)
	}
	if detail.State != platform.StateOpen {
		t.Errorf("expected State=open, got %q", detail.State)
	}
	if detail.Requirements == nil || !detail.Requirements.IDVerified {
		t.Errorf("expected Requirements.IDVerified=true, got %+v", detail.Requirements)
	}
	if detail.Facts == nil || detail.Facts.StartedAt == nil || detail.Facts.OffersBounty == nil || !*detail.Facts.OffersBounty {
		t.Errorf("unexpected Facts: %+v", detail.Facts)
	}
}

func TestFetchScopeDetailAutoJoinFlows(t *testing.T) {
	t.Run("AutoJoin true joins unjoined project with use_ticket=0 and refetches", func(t *testing.T) {
		var joined atomic.Bool
		var gotUseTicket atomic.Int32
		gotUseTicket.Store(-1)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/web/account/basic-info":
				_, _ = w.Write(readFixture(t, "basic_info_ok.json"))
			case "/hacker/project/detail":
				if joined.Load() {
					_, _ = w.Write(readFixture(t, "detail_joined.json"))
				} else {
					_, _ = w.Write(readFixture(t, "detail_hidden.json"))
				}
			case "/hacker/project/join":
				var body struct {
					ProjectID int64 `json:"project_id"`
					UseTicket int   `json:"use_ticket"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				gotUseTicket.Store(int32(body.UseTicket))
				joined.Store(true)
				_, _ = w.Write([]byte(`{"code":200,"data":{"result":true},"msg":"success"}`))
			case "/hacker/project/detail/assets":
				_, _ = w.Write(readFixture(t, "detail_assets.json"))
			}
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, Session: "sample-token", AutoJoin: true})
		entries, _, err := c.FetchScopeDetail(context.Background(), platform.Program{Handle: "10001"})
		if err != nil {
			t.Fatalf("FetchScopeDetail: %v", err)
		}
		if !joined.Load() {
			t.Fatal("expected POST /hacker/project/join to be called")
		}
		if gotUseTicket.Load() != 0 {
			t.Errorf("expected use_ticket=0, got %d", gotUseTicket.Load())
		}
		if len(entries) == 0 {
			t.Fatal("expected unlocked scope entries after auto-join")
		}
	})

	t.Run("AutoJoin true signs agreement when need_sign_agreement is true", func(t *testing.T) {
		var signed atomic.Bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/web/account/basic-info":
				_, _ = w.Write(readFixture(t, "basic_info_ok.json"))
			case "/hacker/project/detail":
				if signed.Load() {
					_, _ = w.Write(readFixture(t, "detail_joined.json"))
				} else {
					_, _ = w.Write([]byte(`{"code":200,"data":{"id":10001,"is_joined":1,"need_sign_agreement":true,"hideProjectInfo":false,"hall_status":2},"msg":"success"}`))
				}
			case "/hacker/project/mine/sign-agreement":
				signed.Store(true)
				_, _ = w.Write([]byte(`{"code":200,"data":{},"msg":"success"}`))
			case "/hacker/project/detail/assets":
				_, _ = w.Write(readFixture(t, "detail_assets.json"))
			}
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, Session: "sample-token", AutoJoin: true})
		entries, _, err := c.FetchScopeDetail(context.Background(), platform.Program{Handle: "10001"})
		if err != nil {
			t.Fatalf("FetchScopeDetail: %v", err)
		}
		if !signed.Load() {
			t.Fatal("expected POST /hacker/project/mine/sign-agreement to be called")
		}
		if len(entries) == 0 {
			t.Fatal("expected scope entries after signing agreement")
		}
	})

	t.Run("AutoJoin skips take_margin=1 without whitelist", func(t *testing.T) {
		var joinCalled atomic.Bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/web/account/basic-info":
				_, _ = w.Write(readFixture(t, "basic_info_ok.json"))
			case "/hacker/project/detail":
				_, _ = w.Write([]byte(`{"code":200,"data":{"id":30001,"is_joined":0,"hideProjectInfo":true,"take_margin":1,"is_margin_white":false,"margin_amount":500,"hall_status":1},"msg":"success"}`))
			case "/hacker/project/join", "/hacker/project/invite":
				joinCalled.Store(true)
				_, _ = w.Write([]byte(`{"code":200,"data":{},"msg":"success"}`))
			}
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, Session: "sample-token", AutoJoin: true})
		_, _, err := c.FetchScopeDetail(context.Background(), platform.Program{Handle: "30001"})
		if !errors.Is(err, platform.ErrScopeUnavailable) {
			t.Fatalf("expected ErrScopeUnavailable for margin-gated project, got %v", err)
		}
		if joinCalled.Load() {
			t.Fatal("MUST NOT call join/invite on margin-gated project")
		}
	})

	t.Run("AutoJoin false returns ErrScopeUnavailable without joining", func(t *testing.T) {
		var joinCalled atomic.Bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/web/account/basic-info":
				_, _ = w.Write(readFixture(t, "basic_info_ok.json"))
			case "/hacker/project/detail":
				_, _ = w.Write(readFixture(t, "detail_hidden.json"))
			case "/hacker/project/join":
				joinCalled.Store(true)
			}
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, Session: "sample-token", AutoJoin: false})
		_, _, err := c.FetchScopeDetail(context.Background(), platform.Program{Handle: "10001"})
		if !errors.Is(err, platform.ErrScopeUnavailable) {
			t.Fatalf("expected ErrScopeUnavailable when AutoJoin=false, got %v", err)
		}
		if joinCalled.Load() {
			t.Fatal("MUST NOT call /join when AutoJoin=false")
		}
	})
}

func TestInvitesListAndAccept(t *testing.T) {
	t.Run("unauthenticated returns ErrNoWebLogin", func(t *testing.T) {
		c := newTestClient(t, Options{PublicOnly: true})
		if _, err := c.ListInvites(context.Background()); !errors.Is(err, platform.ErrNoWebLogin) {
			t.Fatalf("expected ErrNoWebLogin, got %v", err)
		}
		if err := c.AcceptInvite(context.Background(), "20002"); !errors.Is(err, platform.ErrNoWebLogin) {
			t.Fatalf("expected ErrNoWebLogin, got %v", err)
		}
	})

	t.Run("deduplicates invites and blocks margin-gated AcceptInvite", func(t *testing.T) {
		var accepted atomic.Int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/web/account/basic-info":
				_, _ = w.Write(readFixture(t, "basic_info_ok.json"))
			case "/hacker/center/invitation":
				_, _ = w.Write([]byte(`{"code":200,"data":{"list":[{"project_id":20002,"invite_code":"INV-SAMPLE-01"},{"project_id":20003,"take_margin":1,"is_margin_white":false}]},"msg":"success"}`))
			case "/hacker/project/mine/list":
				_, _ = w.Write([]byte(`{"code":200,"data":{"current_page":1,"last_page":1,"data":[{"id":20002,"task_title":"Beta","is_invited":1,"invite_code":"INV-SAMPLE-01"}]},"msg":"success"}`))
			case "/hacker/project/detail":
				if r.URL.Query().Get("id") == "20003" {
					_, _ = w.Write([]byte(`{"code":200,"data":{"id":20003,"take_margin":1,"is_margin_white":false,"margin_amount":500},"msg":"success"}`))
				} else {
					_, _ = w.Write([]byte(`{"code":200,"data":{"id":20002,"take_margin":0,"is_margin_white":false},"msg":"success"}`))
				}
			case "/hacker/project/invite":
				var body struct {
					ProjectID int64 `json:"project_id"`
					Status    int   `json:"status"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body.Status == 1 {
					accepted.Store(body.ProjectID)
				}
				_, _ = w.Write([]byte(`{"code":200,"data":{},"msg":"success"}`))
			}
		}))
		defer srv.Close()

		c := newTestClient(t, Options{BaseURL: srv.URL, Session: "sample-token"})
		invites, err := c.ListInvites(context.Background())
		if err != nil {
			t.Fatalf("ListInvites: %v", err)
		}
		if len(invites) != 2 {
			t.Fatalf("expected 2 deduplicated invites, got %d (%+v)", len(invites), invites)
		}

		// Margin-gated invite must fail cleanly without POSTing.
		if err := c.AcceptInvite(context.Background(), "20003"); err == nil || !strings.Contains(err.Error(), "margin deposit") {
			t.Fatalf("expected margin deposit error for 20003, got %v", err)
		}
		if accepted.Load() != 0 {
			t.Fatalf("expected no POST for margin-gated invite, got %d", accepted.Load())
		}

		// Regular invite accepts cleanly.
		if err := c.AcceptInvite(context.Background(), "20002"); err != nil {
			t.Fatalf("AcceptInvite 20002: %v", err)
		}
		if accepted.Load() != 20002 {
			t.Errorf("expected POST accept for 20002, got %d", accepted.Load())
		}
	})
}

func TestFetchReportsAll5TabsTitleResolutionAndUTC8(t *testing.T) {
	seenTabs := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/bugs/detail") || strings.Contains(r.URL.Path, "/bugs/comments") {
			t.Fatalf("PRIVACY VIOLATION: fetched write-up/comment endpoint %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/web/account/basic-info":
			_, _ = w.Write(readFixture(t, "basic_info_ok.json"))
		case "/hacker/vulnerability/list":
			tab := r.URL.Query().Get("type")
			seenTabs[tab] = true
			if tab == "9" {
				_, _ = w.Write(readFixture(t, "reports_tab9.json"))
				return
			}
			if tab == "0" {
				// Include a duplicate of 900001 to verify cross-tab deduplication plus one out-of-scope report.
				_, _ = w.Write([]byte(`{"code":200,"data":{"current_page":1,"last_page":1,"data":[
					{"id":900001,"bug_title":"Dup Tab","bug_tid":10001,"bug_level":3,"bug_status":4,"bug_valid":1,"bug_money":"3000.00","bug_updata":"2026/08/28 10:30:00"},
					{"id":900004,"bug_title":"Sample OOS","bug_tid":10002,"bug_firm_name":"Example Corp","bug_level":4,"bug_final_level_name":"严重","bug_status":5,"bug_status_desc":"已忽略","bug_valid":3,"valid":"超出范围","bug_updata":"2026-08-27 16:00:00","update_at":"2026-08-28 00:00:00"}
				]},"msg":"success"}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":200,"data":{"current_page":1,"last_page":1,"data":[]},"msg":"success"}`))
		case "/hacker/vulnerability/projects/submitted":
			if r.URL.Query().Get("type") == "9" {
				_, _ = w.Write(readFixture(t, "submitted_projects_tab9.json"))
				return
			}
			_, _ = w.Write([]byte(`{"code":200,"data":{"current_page":1,"last_page":1,"data":[]},"msg":"success"}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, Options{BaseURL: srv.URL, Session: "sample-token"})
	if !c.ReportsConfigured() {
		t.Fatal("expected ReportsConfigured()=true when authenticated")
	}

	var saved []platform.Report
	complete, err := c.FetchReports(context.Background(), nil, 50, func(_ context.Context, batch []platform.Report) error {
		saved = append(saved, batch...)
		return nil
	})
	if err != nil {
		t.Fatalf("FetchReports: %v", err)
	}
	if !complete {
		t.Fatal("expected complete=true")
	}

	for _, tab := range []string{"0", "9", "3", "5", "11"} {
		if !seenTabs[tab] {
			t.Errorf("tab %s was not queried", tab)
		}
	}

	// 900001 (deduped), 900002, 900003, 900004 = 4 unique reports.
	if len(saved) != 4 {
		t.Fatalf("got %d reports, want 4", len(saved))
	}

	byKey := map[string]platform.Report{}
	for _, r := range saved {
		byKey[r.Key] = r
	}

	// Note: tab 0 was queried before tab 9, wait — let's check 900002 (only in tab 9) for title resolution,
	// and check 900001/900002/900003/900004 fields!
	r2 := byKey["900002"]
	if r2.ProgramName != "Acme Security Response Center" {
		t.Errorf("expected resolved Enterprise SRC title for 900002, got %q", r2.ProgramName)
	}
	if r2.State != platform.ReportDuplicate || r2.Severity != platform.SeverityMedium {
		t.Errorf("unexpected 900002 state/sev: %q / %q", r2.State, r2.Severity)
	}

	// Check UTC+8 -> UTC conversion: 2026/08/29 08:00:00 CST == 2026-08-29T00:00:00Z UTC.
	wantSub := time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)
	if !r2.SubmittedAt.Equal(wantSub) {
		t.Errorf("900002 SubmittedAt = %v, want %v", r2.SubmittedAt, wantSub)
	}

	r3 := byKey["900003"]
	if r3.WaitingOn != "you" || r3.Severity != platform.SeverityLow {
		t.Errorf("unexpected 900003 waiting/sev: %q / %q", r3.WaitingOn, r3.Severity)
	}

	r4 := byKey["900004"]
	if r4.State != platform.ReportOutOfScope || r4.Severity != platform.SeverityCritical {
		t.Errorf("unexpected 900004 state/sev: %q / %q", r4.State, r4.Severity)
	}
	// 2026-08-27 16:00:00 CST == 2026-08-27T08:00:00Z UTC.
	if want := time.Date(2026, 8, 27, 8, 0, 0, 0, time.UTC); !r4.SubmittedAt.Equal(want) {
		t.Errorf("900004 SubmittedAt = %v, want %v", r4.SubmittedAt, want)
	}

	r1 := byKey["900001"]
	if r1.State != platform.ReportResolved || r1.Severity != platform.SeverityHigh {
		t.Errorf("unexpected 900001 state/sev: %q / %q", r1.State, r1.Severity)
	}
	if len(r1.Payments) == 0 || r1.Payments[0].Currency != "CNY" || r1.Payments[0].Amount != 3000 {
		t.Errorf("unexpected 900001 payments: %+v", r1.Payments)
	}
}

func TestKeepAliveAndFlexUnmarshaling(t *testing.T) {
	var valid atomic.Bool
	valid.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if valid.Load() {
			_, _ = w.Write(readFixture(t, "basic_info_ok.json"))
		} else {
			_, _ = w.Write(readFixture(t, "basic_info_logged_out.json"))
		}
	}))
	defer srv.Close()

	c := newTestClient(t, Options{BaseURL: srv.URL, Session: "sample-token"})
	if err := c.KeepAlive(context.Background()); err != nil {
		t.Fatalf("KeepAlive valid: %v", err)
	}
	valid.Store(false)
	if err := c.KeepAlive(context.Background()); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("KeepAlive expired: got %v, want ErrSessionExpired", err)
	}

	// Verify flexBool / flexFloat / flexInt64 / flexString handle mixed wire types.
	var s struct {
		B1 flexBool   `json:"b1"`
		B2 flexBool   `json:"b2"`
		F1 flexFloat  `json:"f1"`
		F2 flexFloat  `json:"f2"`
		I1 flexInt64  `json:"i1"`
		S1 flexString `json:"s1"`
		S2 flexString `json:"s2"`
	}
	raw := `{"b1":1,"b2":true,"f1":"1200.50","f2":500,"i1":"42","s1":123,"s2":["ignored"]}`
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("flex unmarshal: %v", err)
	}
	if !s.B1.Bool() || !s.B2.Bool() || s.F1.Float64() != 1200.5 || s.F2.Float64() != 500 || s.I1.Int64() != 42 || s.S1.String() != "123" || s.S2.String() != "" {
		t.Errorf("unexpected flex values: %+v", s)
	}
}
