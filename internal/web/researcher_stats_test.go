package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

func TestHackerOneResearcherStatsCard(t *testing.T) {
	ctx := context.Background()
	withH1 := func(d *Deps) { d.Platforms = append(d.Platforms, PlatformInfo{Name: "hackerone"}) }

	t.Run("empty state shown when hackerone configured without synced stats", func(t *testing.T) {
		e := newTestEnv(t, withH1)
		c := e.client()
		for _, path := range []string{"/reports", "/reports/analytics"} {
			res, body := e.do(c, "GET", path, nil, nil)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("%s status %d", path, res.StatusCode)
			}
			if !strings.Contains(body, `class="panel h1s-card"`) || !strings.Contains(body, "No HackerOne stats synced yet.") {
				t.Errorf("%s missing empty H1 stats card", path)
			}
		}
	})

	t.Run("populated 90d, 1y and all-time metrics and rank cutoff", func(t *testing.T) {
		e := newTestEnv(t, withH1)
		rep90 := 7
		sig1y := -2.67
		repAll, pctAll, rankAll := 1250, 78, 42
		sigAll, impAll := 3.40, 19.25
		rankOver := 250
		if err := e.store.SaveResearcherStats(ctx, "hackerone", platform.ResearcherStats{
			Username:   "alice_example",
			Last90Days: platform.ResearcherWindowStats{Reputation: &rep90},
			PastYear:   platform.ResearcherWindowStats{Signal: &sig1y, Rank: &rankOver},
			AllTime: platform.ResearcherWindowStats{
				Signal:           &sigAll,
				SignalPercentile: &pctAll,
				Impact:           &impAll,
				Reputation:       &repAll,
				Rank:             &rankAll,
			},
		}); err != nil {
			t.Fatal(err)
		}
		c := e.client()
		for _, path := range []string{"/reports", "/reports/analytics"} {
			res, body := e.do(c, "GET", path, nil, nil)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("%s status %d", path, res.StatusCode)
			}
			for _, want := range []string{
				`id="h1s-90d"`, `id="h1s-1y"`, `id="h1s-all"`,
				`90 Days`, `Past Year`, `All time`,
				`h1s-val neg">-2.67</span>`,
				`>3.40</span>`, `>78%</span>`, `>19.25</span>`, `>1,250</span>`, `>#42</span>`,
				`href="https://hackerone.com/alice_example"`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("%s missing %q", path, want)
				}
			}
			if strings.Contains(body, "#250") {
				t.Errorf("%s should not render rank > 100 (#250)", path)
			}
		}

		// Chinese locale renders translated headings and labels cleanly.
		res, body := e.do(c, "GET", "/reports", nil, http.Header{"Accept-Language": {"zh-CN"}})
		if res.StatusCode != http.StatusOK {
			t.Fatalf("/reports (zh) status %d", res.StatusCode)
		}
		for _, want := range []string{"HackerOne 个人指标", "最近 90 天", "过去一年", "全部时间", "声望值"} {
			if !strings.Contains(body, want) {
				t.Errorf("/reports (zh) missing %q", want)
			}
		}
	})

	t.Run("read-only copy hides personal H1 researcher stats", func(t *testing.T) {
		e := newTestEnv(t, withH1, func(d *Deps) { d.ReadOnly = true })
		rep := 7
		if err := e.store.SaveResearcherStats(ctx, "hackerone", platform.ResearcherStats{
			Username:   "alice_example",
			Last90Days: platform.ResearcherWindowStats{Reputation: &rep},
		}); err != nil {
			t.Fatal(err)
		}
		c := e.client()
		res, body := e.do(c, "GET", "/reports", nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status %d", res.StatusCode)
		}
		if strings.Contains(body, "alice_example") || strings.Contains(body, "h1s-card") {
			t.Error("readOnly /reports leaked H1 researcher stats")
		}
	})
}

func TestProgramSignalRequirementCaution(t *testing.T) {
	ctx := context.Background()

	seedH1Program := func(t *testing.T, e *testEnv, handle string, minSig *float64) {
		t.Helper()
		err := e.store.Write(ctx, func(tx *store.Tx) error {
			pid, err := tx.UpsertProgram(ctx, "hackerone", platform.Program{
				Handle: handle,
				Name:   strings.ToUpper(handle),
				URL:    "https://hackerone.com/" + handle,
				State:  platform.StateOpen,
			})
			if err != nil {
				return err
			}
			return tx.SetRequirements(ctx, pid, platform.Requirements{MinSignal: minSig})
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	ptr := func(v float64) *float64 { return &v }

	tests := []struct {
		name           string
		reqMinSignal   float64
		userPastYear   *float64
		readOnly       bool
		lang           string
		wantCaution    bool
		wantMet        bool
		wantSubstrings []string
	}{
		{
			name:           "below positive threshold shows Caution banner and chip",
			reqMinSignal:   2.0,
			userPastYear:   ptr(-2.67),
			wantCaution:    true,
			wantSubstrings: []string{`class="state-banner paused signal-caution"`, `Caution: Signal Requirement`, `>2.0</b>`, `>-2.67</b>`, `class="chip signal-warn"`},
		},
		{
			name:           "below 0.0 threshold shows Caution banner",
			reqMinSignal:   0.0,
			userPastYear:   ptr(-1.50),
			wantCaution:    true,
			wantSubstrings: []string{`class="state-banner paused signal-caution"`, `>0.0</b>`, `>-1.50</b>`},
		},
		{
			name:           "nil past-year signal shows Not yet calculated caution",
			reqMinSignal:   2.0,
			userPastYear:   nil,
			wantCaution:    true,
			wantSubstrings: []string{`class="state-banner paused signal-caution"`, `Not yet calculated`, `>2.0</b>`},
		},
		{
			name:           "meeting threshold shows met banner in Before you test and hides Caution",
			reqMinSignal:   2.0,
			userPastYear:   ptr(3.40),
			wantMet:        true,
			wantSubstrings: []string{`class="before-signal-ok"`, `Signal requirement met`, `3.40`},
		},
		{
			name:           "Chinese locale translates Signal caution completely",
			reqMinSignal:   2.0,
			userPastYear:   ptr(-2.67),
			lang:           "zh",
			wantCaution:    true,
			wantSubstrings: []string{"注意：存在 Signal 门槛限制", "要求 Signal", "你过去一年的 Signal", "-2.67"},
		},
		{
			name:         "readOnly copy never exposes personal Signal comparison",
			reqMinSignal: 2.0,
			userPastYear: ptr(-2.67),
			readOnly:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var opts []func(*Deps)
			if tc.readOnly {
				opts = append(opts, func(d *Deps) { d.ReadOnly = true })
			}
			e := newTestEnv(t, opts...)
			seedH1Program(t, e, "khealth-example", ptr(tc.reqMinSignal))
			if err := e.store.SaveResearcherStats(ctx, "hackerone", platform.ResearcherStats{
				Username: "alice_example",
				PastYear: platform.ResearcherWindowStats{Signal: tc.userPastYear},
			}); err != nil {
				t.Fatal(err)
			}
			c := e.client()
			var hdr http.Header
			if tc.lang != "" {
				hdr = http.Header{"Accept-Language": {tc.lang}}
			}
			res, body := e.do(c, "GET", "/programs/hackerone/khealth-example", nil, hdr)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status %d", res.StatusCode)
			}
			hasCaution := strings.Contains(body, "signal-caution")
			if hasCaution != tc.wantCaution {
				t.Errorf("hasCaution = %v, want %v", hasCaution, tc.wantCaution)
			}
			hasMet := strings.Contains(body, "before-signal-ok")
			if hasMet != tc.wantMet {
				t.Errorf("hasMet = %v, want %v", hasMet, tc.wantMet)
			}
			if tc.readOnly && (strings.Contains(body, "-2.67") || strings.Contains(body, "signal-warn")) {
				t.Error("readOnly leaked personal Signal check")
			}
			for _, sub := range tc.wantSubstrings {
				if !strings.Contains(body, sub) {
					t.Errorf("body missing %q", sub)
				}
			}
		})
	}
}
