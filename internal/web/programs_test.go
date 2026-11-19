package web

import (
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/store"
)

func TestProgramsPage(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	tests := []struct {
		path      string
		want, not []string
	}{
		{"/programs", []string{
			// The rail counts what each choice would show.
			`<a class="fc" href="/programs?wild=1"><span class="box" aria-hidden="true"></span><span class="fl">Has wildcards</span><em>1</em></a>`,
			`<a class="fc" href="/programs?bounty=vdp" title="Programs that don&#39;t pay bounties"><span class="box" aria-hidden="true"></span><span class="fl">VDP only</span><em>1</em></a>`,
			// Columns, with nothing to expand.
			"$100 – $15,000", `<td class="c-comp num nowrap firstday" data-label="Received · 90d"><span class="v">340</span>`, `<span title="First response: 5h">5h</span>`, `<td class="c-scope num" data-label="Assets"><span title="2 in-scope entries; they name 1 wildcard">2</span></td>`, `<span title="$1,250,000 paid in bounties all time">$1.3M</span>`, `<span class="fresh-badge">+1</span>`,
			`class="total" role="status" aria-live="polite">2 open programs</span>`, `<th class="c-resolved num">`, `<linearGradient id="spark-good"`,
			// Paid, reports received and reports resolved share a heading.
			`<th class="c-grp" colspan="3"`, `>Reports received</a></th>`, `>Valid reports</a></th>`,
			// Notes hold requirements and test credentials.
			`>Requirements</th>`,
			// A platform without the number says so, rather than showing a stand-in.
			`share this for the program">—</span>`,
			// With one day saved the numbers show, a dotted line under them
			// until the next day's starts the chart.
			`<td class="c-paid num nowrap firstday" data-label="Paid · 90d"><span class="v"><span title="$41,500 paid in bounties in the last 90 days">$42k</span></span><span class="tchart flat collecting" title="Bounties paid · 90 days: $41,500, saved `,
			"HuntDeck saves it daily: its line starts with the next day&#39;s (",
			// The last column opens the program on its platform.
			`<td class="c-open"><a class="open-ext" href="https://platform.example/acme" target="_blank" rel="noopener noreferrer nofollow" title="Open on Fake"`,
			// Customize, top right, shows or hides any column, wide-screen ones too.
			`data-colpick-toggle><svg class="i" aria-hidden="true"><use href="#i-grid"/></svg><span class="label">Customize</span></button>`,
			`<input type="checkbox" value="launched"> Launched</label>`, `<input type="checkbox" value="open"> Open on the platform</label>`,
			`<table class="fixed plist-table cards" data-cols="program" id="programs-list">`, `data-colpick="program"`, `data-grp="paid comp resolved"`,
			// ... and reorders them; the 90-day columns move as a group.
			`<li class="cgroup" data-key="grp">`, `aria-label="Move Last 90 days"`,
			`<li data-key="comp"><div class="crow"><button type="button" class="grip"`,
			`<script src="/static/columns.js?v=`,
			// Opens best to hunt first.
			`<a class="sort on" href="/programs?rev=1&amp;sort=huntscore" title="How worth hunting`,
			// Open is the default status, on without a filter (and no pill).
			`<a class="fc on" href="/programs" title="Taking reports now (the default)" aria-current="true"><span class="box" aria-hidden="true"></span><span class="fl">Open</span></a>`,
		}, []string{`aria-current="page"><span class="box round"`, `class="vt`, `class="af"`, `class="drawer"`, `class="expand"`, `class="tchart flat"`, "c-trend", "No data", "first response</span>", `class="legend`}},
		{"/programs?bounty=1&sort=huntscore", []string{
			// The view in force is on in the rail; choosing it again clears it.
			`<a class="fc on" href="/programs" title="Highest HuntScore among paying programs" aria-current="page">`,
			`<a class="af" href="/programs?sort=huntscore" title="Remove this filter">Pays bounties`,
			`<a class="link small" href="/programs?sort=huntscore">Clear filters</a>`,
		}, []string{"/api/v1/programs?", ">Columns</button>", ">Save view</button>"}},
		{"/programs?wild=1&top=5000", []string{"1 program</span>", "Acme Corp",
			`<a class="fc on" href="/programs?top=5000" aria-current="true"><span class="box" aria-hidden="true"></span><span class="fl">Has wildcards</span><em>1</em></a>`,
			`<a class="fc" href="/programs?top=20000&amp;wild=1" title="Top bounty, in the program&#39;s own currency"><span class="box" aria-hidden="true"></span><span class="fl">20k or more</span><em>0</em></a>`,
		}, []string{"&lt;script&gt;"}},
		{"/programs?bounty=vdp", []string{"1 program</span>", "&lt;script&gt;", `<span class="chip vdp"`}, []string{"Acme Corp"}},
		{"/programs?apps=1", []string{"0 programs</span>", "No programs match these filters"}, nil},
		{"/programs?sort=quiet&q=acme", []string{`href="/programs?q=acme&amp;sort=received"`, "“acme”",
			`<input type="hidden" name="sort" value="quiet">`}, []string{`<select name="sort"`}},
	}
	for _, tt := range tests {
		_, body := e.do(c, http.MethodGet, tt.path, nil, nil)
		for _, w := range tt.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s lacks %q", tt.path, w)
			}
		}
		for _, n := range tt.not {
			if strings.Contains(body, n) {
				t.Errorf("%s has %q", tt.path, n)
			}
		}
	}

	// The methodology page explains them.
	if resp, body := e.do(c, http.MethodGet, "/methodology", nil, nil); resp.StatusCode != http.StatusOK || !strings.Contains(body, "<h1>Methodology</h1>") ||
		!strings.Contains(body, `<a href="/methodology" aria-current="page">`) {
		t.Errorf("GET /methodology = %d", resp.StatusCode)
	}

	// The API has the list's new columns.
	_, body := e.do(c, http.MethodGet, "/api/v1/programs?q=acme", nil, nil)
	var api struct {
		Programs []struct {
			Bounty   *apiBounty `json:"bounty"`
			NewScope int        `json:"new_scope_30d"`
		} `json:"programs"`
	}
	if err := json.Unmarshal([]byte(body), &api); err != nil || len(api.Programs) != 1 {
		t.Fatalf("API programs: %v: %s", err, body)
	}
	if p := api.Programs[0]; p.Bounty == nil || *p.Bounty.Min != 100 || *p.Bounty.Max != 15000 || p.Bounty.From != "table" || p.NewScope != 1 {
		t.Errorf("API program = %+v, bounty %+v", p, p.Bounty)
	}
}

func TestProgramFilterFrom(t *testing.T) {
	tests := []struct {
		query string
		check func(f store.ProgramFilter) bool
	}{
		{"top=5000", func(f store.ProgramFilter) bool { return f.MinBounty == 5000 }},
		{"top=1234", func(f store.ProgramFilter) bool { return f.MinBounty == 1234 }}, // any amount, not just the chips'
		{"top=-5", func(f store.ProgramFilter) bool { return f.MinBounty == 0 }},
		{"bounty=vdp", func(f store.ProgramFilter) bool { return f.NoBounty && !f.BountyOnly }},
		{"bounty=1", func(f store.ProgramFilter) bool { return f.BountyOnly && !f.NoBounty }},
		{"new=7d&launched=90d", func(f store.ProgramFilter) bool { return !f.NewScopeSince.IsZero() && !f.LaunchedSince.IsZero() }},
		{"new=1y&launched=7d", func(f store.ProgramFilter) bool { return f.NewScopeSince.IsZero() && f.LaunchedSince.IsZero() }},
		{"wild=1&apps=1&api=1&noreq=1", func(f store.ProgramFilter) bool { return f.Wildcards && f.Apps && f.APIs && f.NoRequirements }},
		{"sort=quiet", func(f store.ProgramFilter) bool { return f.Sort == "quiet" }},
		{"sort=bogus", func(f store.ProgramFilter) bool { return f.Sort == "name" }},
	}
	for _, tt := range tests {
		q, _ := url.ParseQuery(tt.query)
		if f := programFilterFrom(q); !tt.check(f) {
			t.Errorf("programFilterFrom(%q) = %+v", tt.query, f)
		}
	}
}

// rolling90 counts the reports in the 90 days up to each point.
func TestRolling90(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	ago := func(d int) time.Time { return now.AddDate(0, 0, -d) }
	times := []time.Time{ago(200), ago(100), ago(80), ago(10), ago(0)}
	got := rolling90(times, ago(14), now, 7*trendStep)
	// Points at 14, 7 and 0 days ago: each counts its own 90 days.
	want := []float64{2, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("got %d points: %+v", len(got), got)
	}
	for i, d := range got {
		if d.Value != want[i] {
			t.Errorf("point %d (%s) = %v, want %v", i, d.At.Format("Jan 2"), d.Value, want[i])
		}
	}
	if !got[len(got)-1].At.Equal(now) || rolling90(times, now, now, trendStep) != nil || len(rolling90(times, ago(14), now, trendStep)) != 15 {
		t.Error("the last point must be now, and an empty span gives none")
	}
}

// A chart's height never stands for less than a fifth of its largest value.
func TestChartRange(t *testing.T) {
	for _, tt := range []struct{ lo, hi, wantLo, wantHi float64 }{
		{17, 18, 15.7, 19.3},      // a small move widens to a fifth of the largest
		{0, 100, 0, 100},          // a big one keeps its own range
		{0.5, 0.51, 0.454, 0.556}, // small values too, staying above zero
		{0, 0, 0, 0},              // nothing to widen
		{-10, -9, -10.5, -8.5},    // negative values may go lower
	} {
		lo, hi := chartRange(tt.lo, tt.hi)
		if math.Abs(lo-tt.wantLo) > 1e-9 || math.Abs(hi-tt.wantHi) > 1e-9 {
			t.Errorf("chartRange(%v, %v) = %v, %v; want %v, %v", tt.lo, tt.hi, lo, hi, tt.wantLo, tt.wantHi)
		}
	}
}

// A trend line says which way it went over the last 90 days (or since its
// first point), in words, and is coloured that way: up green, down red.
func TestBuildSpark(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	series := func(vals ...float64) []store.DayValue { // one a week, ending now
		var out []store.DayValue
		for i, v := range vals {
			out = append(out, store.DayValue{At: now.AddDate(0, 0, -7*(len(vals)-1-i)), Value: v})
		}
		return out
	}
	count := func(n float64) string { return formatCount(n, "") }
	resolved := trendMetric{value: count, words: "resolved in 90 days"}
	received := trendMetric{value: count, words: "reports received in 90 days"}
	tests := []struct {
		name                     string
		days                     []store.DayValue
		m                        trendMetric
		dir, tone, change, since string
	}{
		{"none", nil, resolved, "", "", "", ""},
		{"one point: no line", series(5), received, "", "", "", ""},
		{"short line: since its first point", series(10, 12), resolved, "up", "good", "+20%", "Oct 20"},
		{"up is green whatever the number", series(10, 12), received, "up", "good", "+20%", "Oct 20"},
		{"long line: on 90 days ago", series(1, 10, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 8), resolved, "down", "bad", "−20%", "90 days ago"},
		{"small moves are flat", series(100, 103), resolved, "flat", "flat", "+3%", "Oct 20"},
		{"from nothing", series(0, 4), resolved, "up", "good", "from 0", "Oct 20"},
		{"none all along: no line", series(0, 0), resolved, "", "", "", ""},
		{"none either side of a blip", series(0, 3, 0), resolved, "flat", "flat", "±0%", "Oct 13"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := buildSpark(tt.days, 96, 22, tt.m)
			if sp.Dir != tt.dir || sp.Tone != tt.tone || sp.Change != tt.change || sp.Since != tt.since || (sp.Line != "") != (tt.dir != "") {
				t.Fatalf("got dir %q tone %q change %q since %q line %q", sp.Dir, sp.Tone, sp.Change, sp.Since, sp.Line)
			}
			// Flat stretches keep only their ends, so a line has at most a
			// point a day.
			if tt.dir != "" && (!strings.HasSuffix(sp.Area, " Z") || strings.Count(sp.Line, "L") > len(tt.days)-1 || strings.Count(sp.Line, "L") < 1) {
				t.Errorf("line %q, area %q", sp.Line, sp.Area)
			}
		})
	}
	if sp := buildSpark(series(50, 46), 96, 22, received); sp.Summary != "46 reports received in 90 days now, −8% on Oct 20 (50 then)" {
		t.Errorf("summary = %q", sp.Summary)
	}

	// Each day for the instant tooltip: where it is, and its date and
	// value; days in a row with the same value are one stretch, x0~x1.
	daily := []store.DayValue{{At: now.AddDate(0, 0, -3), Value: 100}, {At: now.AddDate(0, 0, -2), Value: 1112},
		{At: now.AddDate(0, 0, -1), Value: 1112}, {At: now, Value: 1112}}
	sp := buildSpark(daily, 100, 30, received)
	if want := "2|28|Oct 24 · 100;34~98|2|Oct 25 – Oct 27 · 1,112"; sp.Days != want {
		t.Errorf("days %q, want %q", sp.Days, want)
	}
	if want := "M2 28 L34 2 L98 2"; sp.Line != want {
		t.Errorf("line %q, want %q (the flat stretch's middle left out)", sp.Line, want)
	}
	est := buildSpark(series(10, 8), 100, 30, trendMetric{value: func(v float64) string { return "≈" + humanize.Money(v, "USD") }, words: "paid in 90 days"})
	if d := strings.Split(est.Days, ";"); len(d) != 2 || !strings.HasSuffix(d[1], "|Oct 27 · ≈$8") {
		t.Errorf("estimated: %q", est.Days)
	}
}

// The Assets tooltip's entries add up to the number shown; the distinct
// wildcards and domains they name are said apart, as hosts.
func TestAssetsTitle(t *testing.T) {
	tests := []struct {
		p    store.ProgramListItem
		want string
	}{
		{store.ProgramListItem{InScope: 93, Wildcards: 47, Domains: 43, Apps: 2, APIs: 1},
			"93 in-scope entries: 2 apps, 1 API, 90 other; they name 47 wildcards and 43 domains"},
		{store.ProgramListItem{InScope: 7, Domains: 6, APIs: 5}, "7 in-scope entries: 5 APIs, 2 other; they name 6 domains"},
		{store.ProgramListItem{InScope: 2, APIs: 2}, "2 in-scope entries: 2 APIs"},
		{store.ProgramListItem{InScope: 3}, "3 in-scope entries"},
		{store.ProgramListItem{InScope: 1, Wildcards: 1}, "1 in-scope entry; it names 1 wildcard"},
		{store.ProgramListItem{InScope: 1500, Domains: 1200}, "1,500 in-scope entries; they name 1,200 domains"},
		{store.ProgramListItem{}, "No assets in scope"},
	}
	for _, tt := range tests {
		if got := assetsTitle(tt.p); got != tt.want {
			t.Errorf("assetsTitle(%+v) = %q, want %q", tt.p, got, tt.want)
		}
	}
}

// The paid estimate takes each day's payable accepted count times the
// average payout then: the latest saved on or before that day, the earliest
// saved before there was one, and the current one without any.
func TestPaidEstimate(t *testing.T) {
	day := func(n int) time.Time { return time.Date(2026, 10, 20+n, 12, 0, 0, 0, time.UTC) }
	accepted := []store.DayValue{{At: day(0), Value: 10}, {At: day(6), Value: 12}, {At: day(7), Value: 12}}
	avgs := []store.DayValue{{At: day(6).Add(-time.Hour), Value: 100}, {At: day(7).Add(-time.Hour), Value: 200}}
	tests := []struct {
		name string
		avgs []store.DayValue
		want []float64
	}{
		{"saved averages", avgs, []float64{1000, 1200, 2400}},
		{"none saved", nil, []float64{500, 600, 600}},
	}
	for _, tt := range tests {
		got := paidEstimate(accepted, tt.avgs, 50)
		for i, d := range got {
			if d.Value != tt.want[i] || !d.At.Equal(accepted[i].At) {
				t.Errorf("%s: point %d = %v, want %v", tt.name, i, d.Value, tt.want[i])
			}
		}
	}
}

func TestAvgBounty(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	tests := []struct {
		p           store.ProgramListItem
		want, title string
	}{
		{store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: "hackerone"}, StatsCurrency: "USD", AvgBountyLow: f(1600), AvgPayout: f(2500)},
			"$1.6k – $2.5k", "HackerOne's average bounty: $1,600 – $2,500"},
		{store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: "hackerone"}, StatsCurrency: "USD", AvgBountyLow: f(200), AvgPayout: f(300)},
			"$200 – $300", "HackerOne's average bounty: $200 – $300"},
		{store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: "bugcrowd"}, StatsCurrency: "USD", AvgBountyLow: f(34906.45), AvgPayout: f(34906.45)},
			"$35k", "Bugcrowd's average payout over the last 3 months: $34,906"},
		{store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: "yeswehack"}, StatsCurrency: "EUR", AvgBountyLow: f(1165.43), AvgPayout: f(1165.43)},
			"€1.2k", "YesWeHack's average reward: €1,165"},
		{store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: "intigriti"}}, "", ""},
	}
	for _, tt := range tests {
		if got, title := avgBounty(tt.p), avgBountyTitle(tt.p); got != tt.want || title != tt.title {
			t.Errorf("%s: avgBounty = %q (%q), want %q (%q)", tt.p.Platform, got, title, tt.want, tt.title)
		}
	}
}

func TestCountedTitle(t *testing.T) {
	i := func(n int) *int { return &n }
	tests := []struct {
		p     store.ProgramListItem
		what  string
		est   bool
		title string
	}{
		{store.ProgramListItem{Platform: "intigriti"}, "bounties paid", false, ""},
		{store.ProgramListItem{Platform: "yeswehack", CountedDays: i(0), Reports7d: i(14)}, "reports received", true,
			"Estimate: YesWeHack's last-7-days count (14) × 90/7. Counted exactly once HuntDeck has saved 90 days of totals; see Methodology."},
		{store.ProgramListItem{Platform: "yeswehack", CountedDays: i(1), Reports7d: i(14)}, "reports received", true,
			"Estimate: reports received over the 1 day of saved totals, plus YesWeHack's last-7-days count (14) for the other 89 days. Counted exactly once HuntDeck has saved 90 days of totals; see Methodology."},
		{store.ProgramListItem{Platform: "intigriti", CountedDays: i(30)}, "bounties paid", true,
			"Estimate: bounties paid over the 30 days of saved totals, scaled to 90 days. Counted exactly once HuntDeck has saved 90 days of totals; see Methodology."},
		{store.ProgramListItem{Platform: "intigriti", CountedDays: i(0), TotalReports: i(241), Points90d: i(835), PointsAll: i(2159), Hunters90d: i(42)}, "reports received", true,
			"Estimate: Intigriti's all-time total (241) × the share of the program's leaderboard points earned in the last 90 days: 835 of 2,159, 39%, by 42 researchers. Points come from accepted reports and their duplicates, by severity. Counted exactly once HuntDeck has saved 90 days of totals; see Methodology."},
		{store.ProgramListItem{Platform: "intigriti", CountedDays: i(91), Reports7d: i(3), Points90d: i(1), PointsAll: i(10)}, "reports received", false,
			"Counted: reports received in the last 90 days, from Intigriti's all-time total, which HuntDeck saves daily"},
	}
	for _, tt := range tests {
		if got := countedTitle(tt.p, tt.what); got != tt.title {
			t.Errorf("countedTitle(%v, %q) = %q, want %q", tt.p.CountedDays, tt.what, got, tt.title)
		}
		if got := tt.p.Paid90dIsEstimate(); got != tt.est {
			t.Errorf("Paid90dIsEstimate(%v) = %v", tt.p.CountedDays, got)
		}
	}
}
