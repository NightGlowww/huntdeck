package yeswehack

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

func fetchReports(t *testing.T, c *Client, known map[string]string, maxDetails int) ([]platform.Report, bool, error) {
	t.Helper()
	var got []platform.Report
	complete, err := c.FetchReports(context.Background(), known, maxDetails, func(_ context.Context, rs []platform.Report) error {
		got = append(got, rs...)
		return nil
	})
	return got, complete, err
}

func TestReportsConfigured(t *testing.T) {
	f := newFake(t)
	tests := []struct {
		name string
		opts Options
		want bool
	}{
		{name: "public_only", opts: Options{}, want: false},
		{name: "token_only", opts: Options{Token: jwt(t0.Add(time.Hour))}, want: true},
		{name: "email_password", opts: Options{Email: testEmail, Password: testPassword, TOTPSecret: testSecret}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := f.client(t, tc.opts)
			if got := c.ReportsConfigured(); got != tc.want {
				t.Fatalf("ReportsConfigured() = %v, want %v", got, tc.want)
			}
			if !tc.want {
				if _, complete, err := fetchReports(t, c, nil, 10); err == nil || complete {
					t.Fatalf("public-only FetchReports: complete=%v err=%v, want error", complete, err)
				}
			}
		})
	}
}

func TestYWHReportStateAndSeverity(t *testing.T) {
	stateCases := []struct {
		raw  string
		want string
	}{
		{"new", platform.ReportNew},
		{"more_info", platform.ReportNew},
		{"under_review", platform.ReportTriaged},
		{"accepted", platform.ReportAccepted},
		{"ask_verif", platform.ReportAccepted},
		{"resolved", platform.ReportResolved},
		{"wont_fix", platform.ReportAcceptedRisk},
		{"rtfs", platform.ReportAcceptedRisk},
		{"duplicate", platform.ReportDuplicate},
		{"informative", platform.ReportInformative},
		{"out_of_scope", platform.ReportOutOfScope},
		{"spam", platform.ReportSpam},
		{"not_applicable", platform.ReportNotApplicable},
		{"invalid", platform.ReportNotApplicable},
		{"auto_close", platform.ReportNotApplicable},
		{"future_unknown_state", platform.ReportNew},
	}
	for _, tc := range stateCases {
		if got := ywhReportState(tc.raw); got != tc.want {
			t.Errorf("ywhReportState(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}

	sevCases := []struct {
		raw  string
		want string
	}{
		{"none", platform.SeverityNone},
		{"LOW", platform.SeverityLow},
		{"Medium", platform.SeverityMedium},
		{"high", platform.SeverityHigh},
		{"CRITICAL", platform.SeverityCritical},
		{"", ""},
		{"unknown", ""},
	}
	for _, tc := range sevCases {
		if got := ywhSeverity(tc.raw); got != tc.want {
			t.Errorf("ywhSeverity(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

type reportRouteHandler func(w http.ResponseWriter, r *http.Request)

func newReportFake(t *testing.T, routes map[string]reportRouteHandler) *fakeYWH {
	t.Helper()
	f := newFake(t)
	f.requireTok = true
	baseServe := f.serve
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" || r.URL.Path == "/account/totp" {
			baseServe(w, r)
			return
		}
		f.mu.Lock()
		f.hits[r.URL.Path]++
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		authed := f.valid[auth]
		f.mu.Unlock()
		if !authed {
			http.Error(w, `{"code":401,"message":"Invalid JWT Token"}`, http.StatusUnauthorized)
			return
		}
		if h, ok := routes[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "application/json")
			h(w, r)
			return
		}
		baseServe(w, r)
	})
	return f
}

func TestFetchReportsPaginationCollabDedupAndDetails(t *testing.T) {
	routes := map[string]reportRouteHandler{
		"/v2/hunter/reports": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("hunter reports method = %s, want POST", r.Method)
			}
			if r.URL.Query().Get("resultsPerPage") != "100" {
				t.Fatalf("resultsPerPage = %q, want 100", r.URL.Query().Get("resultsPerPage"))
			}
			switch r.URL.Query().Get("page") {
			case "1":
				_, _ = w.Write([]byte(`{
					"pagination":{"nb_pages":2,"nb_results":3},
					"items":[
						{
							"id":101,
							"local_id":"YWH-2026-0101",
							"title":"Stored XSS in admin portal",
							"description":"NEVER keep description",
							"poc":"NEVER keep PoC",
							"program":{"slug":"acme-bug-bounty","title":"Acme Bug Bounty"},
							"status":{"workflow_state":"resolved"},
							"cvss":{"criticity":"HIGH","score":7.5},
							"reward":125050,
							"currency":"",
							"marked_as":"R",
							"created_at":"2026-10-01T10:00:00+00:00",
							"changed_at":"2026-10-05T14:00:00+00:00",
							"accepted_at":"2026-10-02T09:00:00+00:00",
							"resolved_at":"2026-10-05T14:00:00+00:00"
						},
						{
							"id":102,
							"local_id":"YWH-2026-0102",
							"title":"SSRF on webhook.example.com",
							"program":{"slug":"globex-private","title":"Globex Private"},
							"status":{"workflow_state":"more_info"},
							"cvss":{"criticity":"medium","score":5.3},
							"reward":0,
							"currency":"USD",
							"marked_as":"U",
							"created_at":"2026-10-10T08:00:00+00:00",
							"changed_at":"2026-10-12T12:00:00+00:00"
						}
					]
				}`))
			case "2":
				_, _ = w.Write([]byte(`{
					"pagination":{"nb_pages":2,"nb_results":3},
					"items":[
						{
							"id":100,
							"local_id":"YWH-2026-0100",
							"title":"Open redirect on login.example.com",
							"program":{"slug":"initech-vdp","title":"Initech VDP"},
							"status":{"workflow_state":"duplicate"},
							"cvss":{"criticity":"low","score":3.1},
							"reward":50000,
							"currency":"EUR",
							"marked_as":"R",
							"created_at":"2026-09-20T09:00:00+00:00",
							"changed_at":"2026-09-22T11:00:00+00:00"
						}
					]
				}`))
			default:
				http.NotFound(w, r)
			}
		},
		"/collaborator/reports": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("collaborator reports method = %s, want GET", r.Method)
			}
			_, _ = w.Write([]byte(`{
				"pagination":{"nb_pages":1,"nb_results":2},
				"items":[
					{
						"id":101,
						"local_id":"YWH-2026-0101",
						"title":"Duplicate listing of 101",
						"program":{"slug":"acme-bug-bounty","title":"Acme Bug Bounty"},
						"status":{"workflow_state":"resolved"},
						"cvss":{"criticity":"HIGH","score":7.5},
						"reward":125050,
						"marked_as":"R",
						"created_at":"2026-10-01T10:00:00+00:00",
						"changed_at":"2026-10-05T14:00:00+00:00"
					},
					{
						"id":103,
						"local_id":"YWH-2026-0103",
						"title":"IDOR on collab.example.com",
						"program":{"slug":"acme-bug-bounty","title":"Acme Bug Bounty"},
						"status":{"workflow_state":"under_review"},
						"cvss":{"criticity":"critical","score":9.1},
						"reward":0,
						"marked_as":"U",
						"created_at":"2026-10-15T10:00:00+00:00",
						"changed_at":"2026-10-16T10:00:00+00:00"
					}
				]
			}`))
		},
		"/reports/103": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":{"workflow_state":"under_review"},"cvss":{"criticity":"critical","score":9.1},"reward":0}`))
		},
		"/reports/103/logs": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{
				"items":[
					{"id":1,"created_at":"2026-10-15T12:00:00+00:00","type":"status-update","author":{"role":"yeswehack","username":"NEVER_NAME"},"old_status":{"workflow_state":"new"},"new_status":{"workflow_state":"under_review"}},
					{"id":2,"created_at":"2026-10-16T10:00:00+00:00","type":"comment","message":"NEVER_BODY","author":{"role":"hunter","username":"NEVER_HUNTER"}}
				]
			}`))
		},
		"/reports/102": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":{"workflow_state":"more_info"},"cvss":{"criticity":"medium","score":5.3},"reward":0,"currency":"USD"}`))
		},
		"/reports/102/logs": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{
				"items":[
					{"id":3,"created_at":"2026-10-11T09:00:00+00:00","type":"comment","message":"NEVER_BODY","author":{"role":"business_unit","username":"NEVER_STAFF"}}
				]
			}`))
		},
		"/reports/101": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":{"workflow_state":"resolved"},"cvss":{"criticity":"high","score":7.5},"reward":125050,"accepted_at":"2026-10-02T09:00:00+00:00","closed_at":"2026-10-05T15:00:00+00:00"}`))
		},
		"/reports/101/logs": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{
				"items":[
					{"id":10,"created_at":"2026-10-02T09:00:00+00:00","type":"status-update","author":{"role":"yeswehack"},"old_status":"new","new_status":"accepted"},
					{"id":11,"created_at":"2026-10-03T10:00:00+00:00","type":"cvss-update","author":{"role":"yeswehack"},"old_cvss":{"criticity":"medium"},"new_cvss":{"criticity":"high"}},
					{"id":12,"created_at":"2026-10-04T11:00:00+00:00","type":"reward","author":{"role":"business_unit"},"reward":125050,"currency":"EUR"},
					{"id":13,"created_at":"2026-10-04T12:00:00+00:00","type":"comment","message":"NEVER_COMMENT","author":{"role":"business_unit","username":"NEVER_STAFF"}},
					{"id":14,"created_at":"2026-10-04T13:00:00+00:00","type":"comment","message":"NEVER_COMMENT","author":{"role":"hunter","username":"NEVER_HUNTER"}}
				]
			}`))
		},
		"/reports/100": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":{"workflow_state":"duplicate"},"duplicate_of":{"id":88,"local_id":"YWH-2026-0088"},"reward":50000,"currency":"EUR","closed_at":"2026-09-22T11:00:00+00:00"}`))
		},
		"/reports/100/logs": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"items":[]}`))
		},
	}

	f := newReportFake(t, routes)
	c := f.client(t, Options{Email: testEmail, Password: testPassword, TOTPSecret: testSecret})

	got, complete, err := fetchReports(t, c, nil, 10)
	if err != nil || !complete {
		t.Fatalf("complete=%v err=%v", complete, err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d reports, want 4 (deduped collaborator 101)", len(got))
	}
	// Sorted newest first: 103, 102, 101, 100.
	if got[0].Key != "103" || got[1].Key != "102" || got[2].Key != "101" || got[3].Key != "100" {
		t.Fatalf("order = [%s %s %s %s], want [103 102 101 100]", got[0].Key, got[1].Key, got[2].Key, got[3].Key)
	}

	r103, r102, r101, r100 := got[0], got[1], got[2], got[3]
	if r103.State != platform.ReportTriaged || !r103.Unread || r103.WaitingOn != "program" || r103.Comments != 1 {
		t.Errorf("r103 = %+v", r103)
	}
	if r102.State != platform.ReportNew || r102.WaitingOn != "you" || !r102.Unread || r102.Comments != 1 {
		t.Errorf("r102 = %+v", r102)
	}
	if r101.State != platform.ReportResolved || r101.ProgramHandle != "acme-bug-bounty" || r101.URL != "https://yeswehack.com/reports/101" {
		t.Errorf("r101 = %+v", r101)
	}
	if r101.Severity != platform.SeverityHigh || r101.SubmittedSeverity != platform.SeverityMedium || !r101.SeverityChanged {
		t.Errorf("r101 severity=%q submitted=%q changed=%v", r101.Severity, r101.SubmittedSeverity, r101.SeverityChanged)
	}
	if r101.CVSS == nil || *r101.CVSS != 7.5 {
		t.Errorf("r101 CVSS = %v, want 7.5", r101.CVSS)
	}
	if len(r101.Payments) != 1 || r101.Payments[0].Amount != 1250.50 || r101.Payments[0].Currency != "EUR" || r101.Payments[0].Ref != "12" {
		t.Errorf("r101 payments = %+v", r101.Payments)
	}
	if r101.Comments != 2 || !r101.Detailed || !r101.PaymentsKnown || r101.FirstResponseAt == nil || r101.TriagedAt == nil || r101.ClosedAt == nil {
		t.Errorf("r101 details: comments=%d detailed=%v paymentsKnown=%v firstResp=%v triaged=%v closed=%v",
			r101.Comments, r101.Detailed, r101.PaymentsKnown, r101.FirstResponseAt, r101.TriagedAt, r101.ClosedAt)
	}
	if r100.State != platform.ReportDuplicate || r100.DuplicateOf != "YWH-2026-0088" {
		t.Errorf("r100 state=%q dup=%q", r100.State, r100.DuplicateOf)
	}
	if len(r100.Payments) != 1 || r100.Payments[0].Ref != "total" || r100.Payments[0].Kind != "bounty" || r100.Payments[0].Amount != 500 || r100.Payments[0].Currency != "EUR" {
		t.Errorf("r100 fallback payment = %+v", r100.Payments)
	}

	// Privacy check: never store description, PoC, message body, or usernames.
	for _, r := range got {
		b, _ := json.Marshal(r)
		if strings.Contains(string(b), "NEVER") {
			t.Errorf("report %s leaked private text: %s", r.Key, string(b))
		}
	}

	// Marker skip: unchanged reports are not re-fetched; changed one is.
	known := map[string]string{}
	for _, r := range got {
		known[r.Key] = r.Marker
	}
	known["100"] = "stale-marker"
	f.mu.Lock()
	f.hits = map[string]int{}
	f.mu.Unlock()

	again, complete, err := fetchReports(t, c, known, 10)
	if err != nil || !complete || len(again) != 4 {
		t.Fatalf("second run: len=%d complete=%v err=%v", len(again), complete, err)
	}
	f.mu.Lock()
	h101, h102, h103, h100 := f.hits["/reports/101"], f.hits["/reports/102"], f.hits["/reports/103"], f.hits["/reports/100"]
	f.hits = map[string]int{}
	f.mu.Unlock()
	if h101 != 0 || h102 != 0 || h103 != 0 || h100 != 1 {
		t.Errorf("marker skip hits = [101:%d 102:%d 103:%d 100:%d], want only 100 once", h101, h102, h103, h100)
	}

	// maxDetails caps detail fetches to newest first.
	capped, complete, err := fetchReports(t, c, nil, 1)
	if err != nil || !complete || len(capped) != 4 {
		t.Fatalf("capped run: len=%d complete=%v err=%v", len(capped), complete, err)
	}
	f.mu.Lock()
	c103, cOther := f.hits["/reports/103"], f.hits["/reports/102"]+f.hits["/reports/101"]+f.hits["/reports/100"]
	f.mu.Unlock()
	if c103 != 1 || cOther != 0 {
		t.Errorf("capped hits = [103:%d other:%d], want only 103 once", c103, cOther)
	}
	if !capped[0].Detailed || capped[1].Detailed || capped[2].Detailed || capped[3].Detailed {
		t.Errorf("capped Detailed flags = [%v %v %v %v], want [true false false false]",
			capped[0].Detailed, capped[1].Detailed, capped[2].Detailed, capped[3].Detailed)
	}
}

func TestFetchReportsCollaboratorIgnoredAndAuthRetryAndFailures(t *testing.T) {
	tests := []struct {
		name           string
		collabStatus   int
		hunterFail5xx  bool
		revokeFirstTok bool
		wantComplete   bool
		wantErr        bool
		wantCount      int
	}{
		{
			name:         "collab_403_ignored",
			collabStatus: http.StatusForbidden,
			wantComplete: true,
			wantCount:    1,
		},
		{
			name:         "collab_404_ignored",
			collabStatus: http.StatusNotFound,
			wantComplete: true,
			wantCount:    1,
		},
		{
			name:           "401_drops_token_and_retries_login",
			collabStatus:   http.StatusNotFound,
			revokeFirstTok: true,
			wantComplete:   true,
			wantCount:      1,
		},
		{
			name:          "5xx_listing_fails",
			collabStatus:  http.StatusNotFound,
			hunterFail5xx: true,
			wantComplete:  false,
			wantErr:       true,
			wantCount:     0,
		},
		{
			name:         "collab_5xx_fails",
			collabStatus: http.StatusInternalServerError,
			wantComplete: false,
			wantErr:      true,
			wantCount:    0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var f *fakeYWH
			routes := map[string]reportRouteHandler{
				"/v2/hunter/reports": func(w http.ResponseWriter, r *http.Request) {
					if tc.hunterFail5xx {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					_, _ = w.Write([]byte(`{
						"pagination":{"nb_pages":1,"nb_results":1},
						"items":[{
							"id":201,
							"local_id":"YWH-2026-0201",
							"title":"Reflected XSS on app.example.com",
							"program":{"slug":"acme-bug-bounty","title":"Acme Bug Bounty"},
							"status":{"workflow_state":"accepted"},
							"cvss":{"criticity":"medium","score":6.1},
							"reward":25000,
							"currency":"EUR",
							"marked_as":"R",
							"created_at":"2026-10-20T10:00:00+00:00",
							"changed_at":"2026-10-21T10:00:00+00:00"
						}]
					}`))
				},
				"/collaborator/reports": func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.collabStatus)
				},
				"/reports/201": func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte(`{"status":{"workflow_state":"accepted"},"reward":25000}`))
				},
				"/reports/201/logs": func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte(`{"items":[]}`))
				},
			}
			f = newReportFake(t, routes)
			opts := Options{Email: testEmail, Password: testPassword, TOTPSecret: testSecret}
			if tc.revokeFirstTok {
				// Seed an initial token not in f.valid so the first POST gets 401 and relogs in.
				opts.Token = jwt(t0.Add(time.Hour))
			}
			c := f.client(t, opts)
			got, complete, err := fetchReports(t, c, nil, 10)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr=%v", err, tc.wantErr)
			}
			if complete != tc.wantComplete {
				t.Fatalf("complete = %v, want %v", complete, tc.wantComplete)
			}
			if len(got) != tc.wantCount {
				t.Fatalf("got %d reports, want %d", len(got), tc.wantCount)
			}
			if tc.revokeFirstTok && f.logins != 1 {
				t.Errorf("logins = %d, want 1 after 401 retry", f.logins)
			}
			if tc.wantCount == 1 {
				if len(got[0].Payments) != 1 || got[0].Payments[0].Amount != 250 || got[0].Payments[0].Currency != "EUR" {
					t.Errorf("payments = %+v, want 250 EUR", got[0].Payments)
				}
			}
		})
	}
}

func TestFetchReportsDetailFailureStillSavesListing(t *testing.T) {
	routes := map[string]reportRouteHandler{
		"/v2/hunter/reports": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{
				"pagination":{"nb_pages":1,"nb_results":1},
				"items":[{
					"id":301,
					"local_id":"YWH-2026-0301",
					"title":"CSRF on settings.example.com",
					"program":{"slug":"acme-bug-bounty","title":"Acme Bug Bounty"},
					"status":{"workflow_state":"new"},
					"cvss":{"criticity":"low","score":3.5},
					"reward":0,
					"marked_as":"R",
					"created_at":"2026-10-20T10:00:00+00:00",
					"changed_at":"2026-10-20T10:00:00+00:00"
				}]
			}`))
		},
		"/collaborator/reports": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
		"/reports/301": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
	}
	f := newReportFake(t, routes)
	c := f.client(t, Options{Email: testEmail, Password: testPassword, TOTPSecret: testSecret})
	got, complete, err := fetchReports(t, c, nil, 10)
	if err == nil || !complete || len(got) != 1 {
		t.Fatalf("got=%d complete=%v err=%v", len(got), complete, err)
	}
	if got[0].Detailed || got[0].PaymentsKnown {
		t.Errorf("detailed=%v paymentsKnown=%v, want false after detail 5xx", got[0].Detailed, got[0].PaymentsKnown)
	}
	if got[0].State != platform.ReportNew {
		t.Errorf("state = %q, want new", got[0].State)
	}
}
