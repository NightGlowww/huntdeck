package hackerone

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
)

func TestFetchResearcherStats(t *testing.T) {
	sig90, imp90 := 1.25, 12.5
	pctSig90, pctImp90 := 68.4, 52.0
	rep90, rank90 := 42, 87

	sig1y := -2.67
	rep1y, rank1y := -7, 450

	sigAll, impAll := 3.14, 18.0
	pctSigAll := 80.0
	repAll, rankAll := 120, 45

	reset := true

	tests := []struct {
		name         string
		noWeb        bool
		handler      func(t *testing.T, bodyQuery string, vars map[string]any, sess string, w http.ResponseWriter)
		wantErr      bool
		wantNoWeb    bool
		wantUsername string
		check        func(t *testing.T, rs platform.ResearcherStats)
	}{
		{
			name:      "no website login returns ErrNoWebLogin",
			noWeb:     true,
			wantErr:   true,
			wantNoWeb: true,
		},
		{
			name: "combined query decodes all windows, negative signal and float percentiles",
			handler: func(_ *testing.T, q string, _ map[string]any, sess string, w http.ResponseWriter) {
				if sess == "" {
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"me": nil}})
					return
				}
				if strings.Contains(q, "me { id }") {
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"me": map[string]any{"id": "gid://hackerone/User/1"}}})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": map[string]any{
						"me": map[string]any{
							"id":                         "gid://hackerone/User/1",
							"username":                   "alice_example",
							"signal":                     sigAll,
							"signal_percentile":          pctSigAll,
							"impact":                     impAll,
							"impact_percentile":          nil,
							"reputation":                 repAll,
							"rank":                       rankAll,
							"trial_report_reset_process": reset,
							"last_90_days": map[string]any{
								"signal":            sig90,
								"signal_percentile": pctSig90,
								"impact":            imp90,
								"impact_percentile": pctImp90,
								"reputation":        rep90,
								"rank":              rank90,
							},
							"past_year": map[string]any{
								"signal":            sig1y,
								"signal_percentile": nil,
								"impact":            nil,
								"impact_percentile": nil,
								"reputation":        rep1y,
								"rank":              rank1y,
							},
						},
					},
				})
			},
			wantUsername: "alice_example",
			check: func(t *testing.T, rs platform.ResearcherStats) {
				t.Helper()
				if rs.Last90Days.Signal == nil || *rs.Last90Days.Signal != 1.25 {
					t.Errorf("last90 signal = %v, want 1.25", rs.Last90Days.Signal)
				}
				if rs.Last90Days.SignalPercentile == nil || *rs.Last90Days.SignalPercentile != 68 {
					t.Errorf("last90 signal_percentile = %v, want 68", rs.Last90Days.SignalPercentile)
				}
				if rs.PastYear.Signal == nil || *rs.PastYear.Signal != -2.67 {
					t.Errorf("past_year signal = %v, want -2.67", rs.PastYear.Signal)
				}
				if rs.PastYear.Impact != nil || rs.PastYear.SignalPercentile != nil {
					t.Errorf("past_year expected nil impact/pct, got %+v", rs.PastYear)
				}
				if rs.AllTime.Rank == nil || *rs.AllTime.Rank != 45 {
					t.Errorf("all_time rank = %v, want 45", rs.AllTime.Rank)
				}
				if rs.TrialResetOnTriage == nil || !*rs.TrialResetOnTriage {
					t.Errorf("trial_reset_on_triage = %v, want true", rs.TrialResetOnTriage)
				}
			},
		},
		{
			name: "sparse new account keeps nil stats and reputation",
			handler: func(_ *testing.T, q string, _ map[string]any, _ string, w http.ResponseWriter) {
				if strings.Contains(q, "me { id }") {
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"me": map[string]any{"id": "gid://hackerone/User/2"}}})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": map[string]any{
						"me": map[string]any{
							"id":                         "gid://hackerone/User/2",
							"username":                   "bob_example",
							"signal":                     nil,
							"signal_percentile":          nil,
							"impact":                     nil,
							"impact_percentile":          nil,
							"reputation":                 7,
							"rank":                       nil,
							"trial_report_reset_process": nil,
							"last_90_days": map[string]any{
								"signal":     nil,
								"reputation": 7,
							},
							"past_year": map[string]any{
								"signal":     nil,
								"reputation": 7,
							},
						},
					},
				})
			},
			wantUsername: "bob_example",
			check: func(t *testing.T, rs platform.ResearcherStats) {
				t.Helper()
				if rs.Last90Days.Signal != nil || rs.Last90Days.Reputation == nil || *rs.Last90Days.Reputation != 7 {
					t.Errorf("sparse 90d = %+v", rs.Last90Days)
				}
				if rs.AllTime.Rank != nil || rs.AllTime.Reputation == nil || *rs.AllTime.Reputation != 7 {
					t.Errorf("sparse all_time = %+v", rs.AllTime)
				}
			},
		},
		{
			name: "combined graphql schema error falls back to separate snapshot queries",
			handler: func(_ *testing.T, q string, vars map[string]any, _ string, w http.ResponseWriter) {
				switch {
				case strings.Contains(q, "me { id }"):
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"me": map[string]any{"id": "gid://1"}}})
				case strings.Contains(q, "trial_report_reset_process"):
					_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]any{{"message": "Field 'trial_report_reset_process' doesn't exist"}}})
				case strings.HasPrefix(q, "query ResearcherStatsAllTime"):
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"me": map[string]any{"id": "gid://1", "username": "carol_example", "reputation": 15}}})
				case strings.HasPrefix(q, "query UserProfileStatsCardSnapshot"):
					snap, _ := vars["snapshotType"].(string)
					sig := 2.5
					if snap == "last_90_days" {
						sig = 1.0
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"data": map[string]any{
							"user": map[string]any{
								"id":                  "gid://1",
								"statistics_snapshot": map[string]any{"signal": sig, "reputation": 5},
							},
						},
					})
				}
			},
			wantUsername: "carol_example",
			check: func(t *testing.T, rs platform.ResearcherStats) {
				t.Helper()
				if rs.Last90Days.Signal == nil || *rs.Last90Days.Signal != 1.0 {
					t.Errorf("fallback 90d signal = %v, want 1.0", rs.Last90Days.Signal)
				}
				if rs.PastYear.Signal == nil || *rs.PastYear.Signal != 2.5 {
					t.Errorf("fallback 1y signal = %v, want 2.5", rs.PastYear.Signal)
				}
				if rs.AllTime.Reputation == nil || *rs.AllTime.Reputation != 15 {
					t.Errorf("fallback all_time rep = %v, want 15", rs.AllTime.Reputation)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.noWeb {
				c, err := New(Options{Username: "user", Token: "token", ScopesPerMinute: 1e6, StatsPerSecond: 1e6,
					HTTP: httpclient.New(httpclient.Options{RatePerSecond: 1000, Burst: 100, MaxAttempts: 1})})
				if err != nil {
					t.Fatal(err)
				}
				_, err = c.FetchResearcherStats(context.Background())
				if !errors.Is(err, platform.ErrNoWebLogin) {
					t.Fatalf("got err %v, want ErrNoWebLogin", err)
				}
				return
			}

			var sessActive = true
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ck, _ := r.Cookie(sessionCookie)
				sess := ""
				if ck != nil && sessActive && ck.Value == "s1" {
					sess = ck.Value
				}
				switch r.URL.Path {
				case csrfPage:
					_, _ = w.Write([]byte(`<html><head><meta name="csrf-token" content="tok-s1"></head></html>`))
				case "/graphql":
					if r.Header.Get("X-CSRF-Token") != "tok-s1" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					var body struct {
						Query     string         `json:"query"`
						Variables map[string]any `json:"variables"`
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					tc.handler(t, body.Query, body.Variables, sess, w)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()

			jar, _ := cookiejar.New(nil)
			opts := httpclient.Options{RatePerSecond: 1000, Burst: 100, MaxAttempts: 1, Sleep: func(context.Context, time.Duration) error { return nil }}
			webOpts := opts
			webOpts.Jar = jar
			u, _ := url.Parse(srv.URL)
			jar.SetCookies(u, []*http.Cookie{{Name: sessionCookie, Value: "s1", Path: "/"}})
			c, err := New(Options{
				Username: "user", Token: "token", BaseURL: "http://127.0.0.1:1",
				ScopesPerMinute: 1e6, StatsPerSecond: 1e6,
				HTTP:       httpclient.New(opts),
				GraphQLURL: srv.URL + "/graphql",
				Email:      "hunter@example.com", Password: "pw",
				WebHTTP: httpclient.New(webOpts), Jar: jar, WebURL: srv.URL,
				Login: func(context.Context) error { return errors.New("unexpected login") },
			})
			if err != nil {
				t.Fatal(err)
			}

			got, err := c.FetchResearcherStats(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("FetchResearcherStats() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got.Username != tc.wantUsername {
				t.Errorf("username = %q, want %q", got.Username, tc.wantUsername)
			}
			if tc.check != nil {
				tc.check(t, got)
			}
		})
	}
}
