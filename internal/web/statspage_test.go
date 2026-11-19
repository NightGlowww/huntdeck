package web

import (
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/i18n"
	"github.com/mdpsec/huntdeck/internal/store"
)

func TestLinkedDays(t *testing.T) {
	day := func(n int) time.Time { return time.Date(2026, 10, 1+n, 0, 0, 0, 0, time.UTC) }
	pd := func(id int64, n int, v float64) store.ProgramDay {
		return store.ProgramDay{ProgramID: id, Day: day(n), Value: v}
	}
	tests := []struct {
		name string
		days []store.ProgramDay
		end  float64
		want []float64
	}{
		// Program 2 is first saved on day 1: no jump, the line follows program 1.
		{"a program saved later", []store.ProgramDay{pd(1, 0, 10), pd(1, 1, 11), pd(2, 1, 100), pd(1, 2, 12), pd(2, 2, 100)}, 112,
			[]float64{100.9, 111, 112}},
		// Day 1 has one program of three: a poll stopped part way, skipped.
		{"a partial day", []store.ProgramDay{pd(1, 0, 10), pd(2, 0, 10), pd(3, 0, 10), pd(1, 1, 5), pd(1, 2, 10), pd(2, 2, 10), pd(3, 2, 10)}, 30,
			[]float64{30, 30}},
		// The last day is today, still being saved: linked over what it has.
		{"today so far", []store.ProgramDay{pd(1, 0, 10), pd(2, 0, 10), pd(3, 0, 10), pd(1, 1, 12)}, 36,
			[]float64{30, 36}},
		// Days 0 and 1 share no program with day 2: it starts the line again.
		{"no programs in common", []store.ProgramDay{pd(1, 0, 10), pd(1, 1, 20), pd(2, 2, 50), pd(2, 3, 60)}, 60,
			[]float64{50, 60}},
		{"nothing now", []store.ProgramDay{pd(1, 0, 10)}, 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := linkedDays(tt.days, tt.end)
			if len(got) != len(tt.want) {
				t.Fatalf("got %+v, want %v", got, tt.want)
			}
			for i, w := range tt.want {
				if math.Abs(got[i].Value-w) > 0.1 {
					t.Errorf("day %d = %.2f, want %.1f", i, got[i].Value, w)
				}
			}
		})
	}
}

func TestMedianOf(t *testing.T) {
	if m := medianOf(nil); m != nil {
		t.Errorf("empty = %v", *m)
	}
	for _, tt := range []struct {
		v    []float64
		want float64
	}{{[]float64{5, 1, 3}, 3}, {[]float64{4, 1, 3, 2}, 2.5}} {
		if m := medianOf(tt.v); m == nil || *m != tt.want {
			t.Errorf("median %v = %v, want %v", tt.v, m, tt.want)
		}
	}
}

func TestBuildStats(t *testing.T) {
	start := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	months := func(f func(i int) store.PlatformMonth) []store.PlatformMonth {
		out := make([]store.PlatformMonth, overviewMonths)
		for i := range out {
			out[i] = f(i)
		}
		return out
	}
	since := time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)
	overviews := []store.PlatformOverview{
		{Platform: "bugcrowd", Open: 3, Paying: 2, Assets: 30, ValidKind: "accepted", ValidSince: &since,
			Months: months(func(i int) store.PlatformMonth { return store.PlatformMonth{ScopeAdded: 1, Valid: 10, Bounties: 100} })},
		{Platform: "hackerone", Open: 5, Paying: 4, Assets: 50, ValidKind: "resolved", Launches: true,
			Months: months(func(i int) store.PlatformMonth {
				return store.PlatformMonth{ScopeAdded: 2, Launched: 1, Valid: 20, ResolveDays: []float64{float64(i)}}
			})},
		{Platform: "yeswehack", Open: 1, Months: months(func(int) store.PlatformMonth { return store.PlatformMonth{} })},
	}
	paid, recv := 1000.0, 40
	items := []store.ProgramListItem{{ProgramRef: store.ProgramRef{Platform: "hackerone"}, OffersBounty: true}}
	items[0].Paid90d, items[0].Reports90d, items[0].StatsCurrency = &paid, &recv, "USD"
	d := buildStats(overviews, []string{"hackerone", "bugcrowd", "yeswehack"}, items, nil, start)

	if len(d.Rows) != 3 || d.Rows[0].Platform != "hackerone" || d.Rows[1].Platform != "bugcrowd" {
		t.Fatalf("rows not in the platforms' order: %+v", d.Rows)
	}
	if d.Month != "October" {
		t.Errorf("month = %q", d.Month)
	}
	h1, bc, ywh, all := d.Rows[0], d.Rows[1], d.Rows[2], d.All
	if h1.LaunchCell() == nil || bc.LaunchCell() != nil || bc.ResolveCell() != nil || ywh.ValidCell() != nil || ywh.PaidCell() != nil {
		t.Error("a platform without the numbers has cells for them")
	}
	if c := h1.PaidCell(); c == nil || c.N != "$1k" || c.Chart != nil {
		t.Errorf("hackerone paid = %+v", c)
	}
	if c := h1.ReceivedCell(); c == nil || c.N != "40" {
		t.Errorf("hackerone received = %+v", c)
	}
	if all.Open != 9 || all.Paying != 6 || all.Assets != 80 || all.ScopeLast != 3 || all.ValidKind != "valid" || all.ValidLast != 30 {
		t.Errorf("all = %+v", all)
	}
	// Bugcrowd's accepted counts are complete from April, so all of them
	// are too: earlier months have no data.
	if c := all.ValidCell(); c == nil || c.Chart.Months[3].Known || !c.Chart.Months[4].Known {
		t.Errorf("all valid months = %+v", c.Chart.Months)
	}
	if c := bc.BountiesCell(); c == nil || c.N != "$100" {
		t.Errorf("bugcrowd bounties = %+v", c)
	}
}

func TestStatsPlatformPage(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	_, body := e.do(c, http.MethodGet, "/stats", nil, nil)
	if !strings.Contains(body, `<a class="ov-plat" href="/stats/fake"`) {
		t.Error("the stats page's platform row doesn't link to the platform's page")
	}
	// Its page has a heading of its own: the top bar's trail is hidden on a phone.
	if _, body = e.do(c, http.MethodGet, "/stats/fake", nil, nil); strings.Count(body, "<h1") != 1 || !strings.Contains(body, `<h1 class="m-heading">`) {
		t.Error("the platform's stats page lacks its one h1")
	}
	_, body = e.do(c, http.MethodGet, "/stats?visibility=private", nil, nil)
	for _, want := range []string{`<a class="ov-plat" href="/stats/fake?visibility=private"`, `<nav class="seg vis-toggle" aria-label="Which programs">`,
		`<div class="vis-toggle-m">`, `<a class="on" aria-current="true" href="/stats?visibility=private"`} {
		if !strings.Contains(body, want) {
			t.Errorf("/stats?visibility=private lacks %q", want)
		}
	}
	tests := []struct {
		path       string
		status     int
		want, lack []string
	}{
		{"/stats/fake", http.StatusOK, []string{`<a href="/stats">Stats</a>`, `<a class="name" href="/programs/fake/acme" title="Acme Corp">Acme Corp</a>`,
			`&lt;script&gt;alert(1)&lt;/script&gt;`, `<h2>Open programs <span class="count">2</span></h2>`, `href="/programs?platform=fake"`,
			`<a class="sort on" href="/stats/fake?rev=1&amp;sort=paid">Paid</a><small>last 90 days</small>`}, []string{"<script>alert(1)"}},
		{"/stats/fake?bounty=1", http.StatusOK, []string{"Acme Corp", `<span class="count">1</span>`}, []string{"/programs/fake/evil"}},
		{"/stats/fake?q=nothing-matches", http.StatusOK, []string{"programs match. <a class=\"link\" href=\"/stats/fake\">Show them all</a>"}, nil},
		{"/stats/fake?sort=name&rev=1", http.StatusOK, []string{`<a class="sort on rev" href="/stats/fake?sort=name">Program</a>`}, nil},
		{"/stats/nope", http.StatusNotFound, nil, nil},
		// The visibility toggle: acme is public, evil private.
		{"/stats/fake?visibility=private", http.StatusOK, []string{`&lt;script&gt;alert(1)&lt;/script&gt;`, `<a href="/stats?visibility=private">Stats</a>`,
			`<a class="on" aria-current="true" href="/stats/fake?visibility=private" title="Private programs only`, `Private <em>1</em>`,
			`href="/stats/fake?sort=name&amp;visibility=private"`, `<input type="hidden" name="visibility" value="private">`,
			`href="/programs?platform=fake&amp;visibility=private"`}, []string{"Acme Corp"}},
		{"/stats/fake?visibility=public", http.StatusOK, []string{"Acme Corp"}, []string{"/programs/fake/evil"}},
		// The toggle's choices go back to the first page.
		{"/stats/fake?visibility=public&page=2", http.StatusOK, []string{`<a href="/stats/fake" title="Every program you can see">All <em>2</em></a>`}, nil},
		{"/stats/nope?visibility=private", http.StatusNotFound, nil, nil},
	}
	for _, tt := range tests {
		res, body := e.do(c, http.MethodGet, tt.path, nil, nil)
		if res.StatusCode != tt.status {
			t.Errorf("%s = %d, want %d", tt.path, res.StatusCode, tt.status)
		}
		for _, w := range tt.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s lacks %q", tt.path, w)
			}
		}
		for _, l := range tt.lack {
			if strings.Contains(body, l) {
				t.Errorf("%s has %q", tt.path, l)
			}
		}
	}
}

func TestSortStatsRows(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	n := func(v int) *int { return &v }
	row := func(name string, paid float64, recv *int, resolve *float64) platformStats {
		r := platformStats{Item: &store.ProgramListItem{ProgramRef: store.ProgramRef{Name: name}}, ReceivedNow: recv, ResolveLast: resolve}
		if paid > 0 {
			r.Paid = []moneySum{{"USD", paid}}
		}
		return r
	}
	rows := []platformStats{row("b", 100, n(5), f(30)), row("a", 0, nil, nil), row("c", 300, n(9), f(10))}
	names := func() string {
		var s string
		for _, r := range rows {
			s += r.Item.Name
		}
		return s
	}
	tests := []struct {
		key  string
		rev  bool
		want string
	}{
		{"paid", false, "cba"}, {"paid", true, "bca"}, // without a number: last either way
		{"received", false, "cba"}, {"resolve", false, "cba"}, {"resolve", true, "bca"},
		{"name", false, "abc"}, {"name", true, "cba"},
	}
	for _, tt := range tests {
		sortStatsRows(rows, tt.key, tt.rev, "USD")
		if got := names(); got != tt.want {
			t.Errorf("%s rev %v = %s, want %s", tt.key, tt.rev, got, tt.want)
		}
	}
}

// A platform whose valid reports are counted from a few days ago shows the
// days it has with a line from them, and stays out of
// the All row until it covers half the year; one with decision times and
// no resolutions shows those as its days to resolve.
func TestStatsRecentValidAndDecisions(t *testing.T) {
	start := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	months := func(f func(i int) store.PlatformMonth) []store.PlatformMonth {
		out := make([]store.PlatformMonth, overviewMonths)
		for i := range out {
			out[i] = f(i)
		}
		return out
	}
	since := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	overviews := []store.PlatformOverview{
		{Platform: "hackerone", ValidKind: "resolved", Months: months(func(int) store.PlatformMonth {
			return store.PlatformMonth{Valid: 20, ResolveDays: []float64{30}}
		})},
		{Platform: "intigriti", ValidKind: "accepted", ValidSince: &since, Months: months(func(i int) store.PlatformMonth {
			if i >= 10 {
				return store.PlatformMonth{Valid: 7}
			}
			return store.PlatformMonth{}
		})},
	}
	var hours []store.ProgramDay
	for d := range 9 {
		day := time.Date(2026, 10, 28+d, 0, 0, 0, 0, time.UTC)
		for id, h := range []float64{48, 96, 240} { // 2, 4 and 10 days: the median is 4
			hours = append(hours, store.ProgramDay{ProgramID: int64(id), Day: day, Value: h})
		}
	}
	days := map[string]map[string][]store.ProgramDay{store.DayDecisionHours: {"intigriti": hours, "hackerone": hours}}
	d := buildStats(overviews, []string{"hackerone", "intigriti"}, nil, days, start)
	h1, igt := d.Rows[0], d.Rows[1]

	c := igt.ValidCell()
	if c == nil || c.N != "7" || c.AlsoKey != "since" || c.Also != "Oct 28" || c.Chart == nil || c.Wait != nil {
		t.Errorf("intigriti valid cell %+v; want 7 with since Oct 28", c)
	}
	ywhSince := time.Date(2026, 11, 5, 21, 0, 0, 0, time.UTC)
	ywh := buildStats([]store.PlatformOverview{{Platform: "yeswehack", ValidKind: "accepted", ValidSince: &ywhSince,
		Months: make([]store.PlatformMonth, overviewMonths)}}, []string{"yeswehack"}, nil, nil, start).Rows[0].ValidCell()
	if ywh == nil || ywh.N != "" || ywh.AlsoKey != "counted from" || ywh.Also != "Nov 5" || ywh.Chart != nil {
		t.Errorf("yeswehack valid cell %+v; want no number or line, counted from Nov 5", ywh)
	}
	if v := d.All.ValidCell(); v == nil || v.N != "20" {
		t.Errorf("all valid cell %+v; want hackerone's 20 alone", v)
	}
	if d.AllLeftOut != "Intigriti (counted since Oct 2026)" {
		t.Errorf("AllLeftOut = %q; the total should say what it leaves out", d.AllLeftOut)
	}
	r := igt.ResolveCellNoted()
	if r == nil || r.N != "4d" || r.NoteKey != "to decision" || r.Chart == nil || igt.ResolveCell().NoteKey != "" {
		t.Errorf("intigriti resolve cell %+v; want 4d to decision with a line (on the Stats page; on its own page, no note)", r)
	}
	if h1.Decision != nil || d.All.Decision != nil {
		t.Error("decision times shown where there are resolutions, or added up for all platforms")
	}
}

// A platform's page renders while its valid reports are only days old.
func TestStatsPlatformValidWaiting(t *testing.T) {
	since := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	o := store.PlatformOverview{Platform: "intigriti", ValidKind: "accepted", ValidSince: &since, Months: make([]store.PlatformMonth, overviewMonths)}
	d := buildStatsPlatform(o, nil, nil, nil, time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC), "", false, "", false)
	messages, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	pages, err := loadTemplates(func() *time.Location { return time.UTC }, "test", false, readOnlyLinks{}, []string{"en"}, messages)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := pages["en"]["statsplatform"].ExecuteTemplate(&b, "ov-tiles", d.Platform); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"since Oct 28"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("tiles lack %q", want)
		}
	}
}
