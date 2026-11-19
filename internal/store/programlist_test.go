package store

import (
	"context"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

// listFixture stores three programs for the programs list filters:
//   - acme pays up to 20,000 by its table, added a wildcard and an Android
//     app just now, and needs nothing beyond an account;
//   - globex pays 500–3,000 by its listing, has an API known only to be
//     there by now (added_bound), and needs Signal 1;
//   - initech is a VDP with a domain added 60 days ago;
//   - hooli is a VDP you apply to, with no scope.
//
// acme's Hacktivity has one report resolved 10 days ago and one 100 days
// ago; globex's was read and has none; the others' wasn't read.
func listFixture(t *testing.T) (*Store, time.Time) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })
	f := func(v float64) *float64 { return &v }
	old := now.Add(-60 * 24 * time.Hour)
	type entry struct {
		e        platform.ScopeEntry
		baseline bool
	}
	programs := []struct {
		p       platform.Program
		rewards []platform.RewardRow
		req     *platform.Requirements
		entries []entry
	}{
		{platform.Program{Handle: "acme", Name: "Acme", OffersBounty: true, Currency: "USD"},
			[]platform.RewardRow{{Low: platform.RewardRange{Min: 100, Max: 500}, Critical: platform.RewardRange{Min: 5000, Max: 20000}}},
			&platform.Requirements{},
			[]entry{{platform.ScopeEntry{InScope: true, AssetType: "wildcard", Identifier: "*.example.com"}, false},
				{platform.ScopeEntry{InScope: true, AssetType: "android", Identifier: "com.example.app"}, false}}},
		{platform.Program{Handle: "globex", Name: "Globex", OffersBounty: true, Currency: "USD", BountyMin: f(500), BountyMax: f(3000)},
			nil, &platform.Requirements{MinSignal: f(1)},
			[]entry{{platform.ScopeEntry{InScope: true, AssetType: "api", Identifier: "api.example.net"}, true}}},
		{platform.Program{Handle: "initech", Name: "Initech"}, nil, nil,
			[]entry{{platform.ScopeEntry{InScope: true, AssetType: "domain", Identifier: "www.example.org", CreatedAt: &old}, false}}},
		{platform.Program{Handle: "hooli", Name: "Hooli"}, nil, &platform.Requirements{Application: true}, nil},
	}
	err := s.Write(ctx, func(tx *Tx) error {
		for _, p := range programs {
			p.p.URL, p.p.State = "https://example.com/"+p.p.Handle, "open"
			pid, err := tx.UpsertProgram(ctx, "fake", p.p)
			if err != nil {
				return err
			}
			if p.rewards != nil {
				if err := tx.SetRewards(ctx, pid, p.rewards); err != nil {
					return err
				}
			}
			if p.req != nil {
				if err := tx.SetRequirements(ctx, pid, *p.req); err != nil {
					return err
				}
			}
			for i, e := range p.entries {
				id, err := tx.InsertEntry(ctx, pid, p.p.Handle+string(rune('a'+i)), "h", e.e, e.baseline)
				if err != nil {
					return err
				}
				if err := tx.ReplaceTargets(ctx, id, scope.Targets(e.e.AssetType, e.e.Identifier)); err != nil {
					return err
				}
			}
			if err := tx.RefreshScopeCounts(ctx, pid); err != nil {
				return err
			}
		}
		if _, err := tx.tx.ExecContext(ctx, `UPDATE scope_entries SET added_at = ?, added_bound = 1 WHERE identifier = 'api.example.net'`, ts(now)); err != nil {
			return err
		}
		ids := map[string]int64{}
		for _, h := range []string{"acme", "globex"} {
			var id int64
			if err := tx.tx.QueryRowContext(ctx, `SELECT id FROM programs WHERE handle = ?`, h).Scan(&id); err != nil {
				return err
			}
			if err := tx.MarkResolutionsSynced(ctx, id); err != nil {
				return err
			}
			ids[h] = id
		}
		for i, ago := range []int{10, 100} {
			r := platform.Resolution{ID: string(rune('1' + i)), ActivityAt: now.Add(-time.Duration(ago) * 24 * time.Hour), Action: "resolved"}
			if _, err := tx.SaveResolution(ctx, ids["acme"], r); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, now
}

func TestProgramListFilters(t *testing.T) {
	ctx := context.Background()
	s, now := listFixture(t)
	tests := []struct {
		name string
		f    ProgramFilter
		want []string
	}{
		{"everything, by name", ProgramFilter{}, []string{"acme", "globex", "hooli", "initech"}},
		{"top bounty 5k or more", ProgramFilter{MinBounty: 5000}, []string{"acme"}},
		{"top bounty 1k or more: a listing's top counts", ProgramFilter{MinBounty: 1000}, []string{"acme", "globex"}},
		{"VDPs", ProgramFilter{NoBounty: true}, []string{"hooli", "initech"}},
		{"new scope in 7 days: not there-from-the-start or older", ProgramFilter{NewScopeSince: now.Add(-7 * 24 * time.Hour)}, []string{"acme"}},
		{"new scope in 90 days", ProgramFilter{NewScopeSince: now.Add(-90 * 24 * time.Hour)}, []string{"acme", "initech"}},
		{"wildcards", ProgramFilter{Wildcards: true}, []string{"acme"}},
		{"apps", ProgramFilter{Apps: true}, []string{"acme"}},
		{"APIs", ProgramFilter{APIs: true}, []string{"globex"}},
		{"no requirements: unknown ones and applications are left out", ProgramFilter{NoRequirements: true}, []string{"acme"}},
		{"top bounty first, none last", ProgramFilter{Sort: "bounty"}, []string{"acme", "globex", "hooli", "initech"}},
		{"top bounty turned around, none still last, ties A to Z", ProgramFilter{Sort: "bounty", Reverse: true}, []string{"globex", "acme", "hooli", "initech"}},
		{"most new scope first", ProgramFilter{Sort: "fresh"}, []string{"acme", "globex", "hooli", "initech"}},
		{"most resolved first, unread last", ProgramFilter{Sort: "resolved"}, []string{"acme", "globex", "hooli", "initech"}},
		{"least resolved first, unread still last, ties A to Z", ProgramFilter{Sort: "resolved", Reverse: true}, []string{"globex", "acme", "hooli", "initech"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, total, err := s.Programs(ctx, tt.f)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, it := range items {
				got = append(got, it.Handle)
			}
			if !slices.Equal(got, tt.want) || total != len(tt.want) {
				t.Errorf("got %q (total %d), want %q", got, total, tt.want)
			}
		})
	}

	items, _, err := s.Programs(ctx, ProgramFilter{})
	if err != nil {
		t.Fatal(err)
	}
	v := func(p *float64) float64 {
		if p == nil {
			return -1
		}
		return *p
	}
	wants := map[string]struct {
		min, max          float64
		from              string
		fresh, apps, apis int
		resolved          int // -1: not listed
	}{
		"acme":    {100, 20000, PayFromTable, 2, 1, 0, 1},
		"globex":  {500, 3000, PayFromListing, 0, 0, 1, 0},
		"initech": {-1, -1, "", 0, 0, 0, -1},
		"hooli":   {-1, -1, "", 0, 0, 0, -1},
	}
	for _, it := range items {
		w := wants[it.Handle]
		resolved := -1
		if it.Resolved90d != nil {
			resolved = *it.Resolved90d
		}
		if v(it.BountyMin) != w.min || v(it.BountyMax) != w.max || it.BountyFrom != w.from || it.Fresh != w.fresh || it.Apps != w.apps ||
			it.APIs != w.apis || resolved != w.resolved {
			t.Errorf("%s: bounty %v–%v from %q, fresh %d, apps %d, APIs %d, resolved %d; want %+v", it.Handle,
				v(it.BountyMin), v(it.BountyMax), it.BountyFrom, it.Fresh, it.Apps, it.APIs, resolved, w)
		}
	}

	// ID picks one program, whatever the other filters say (its own page).
	for _, it := range items {
		one, total, err := s.Programs(ctx, ProgramFilter{ID: it.ID, NoBounty: it.OffersBounty, State: "removed"})
		if err != nil || total != 1 || len(one) != 1 || one[0].Handle != it.Handle {
			t.Errorf("ID %d (%s): got %d programs (total %d), err %v", it.ID, it.Handle, len(one), total, err)
		}
	}

	// The stats comparison shares the filter, under its own table alias.
	if _, _, err := s.ProgramStatsList(ctx, ProgramFilter{MinBounty: 1000, NewScopeSince: now.Add(-time.Hour), Apps: true, APIs: true, NoRequirements: true}); err != nil {
		t.Errorf("stats list with the new filters: %v", err)
	}
}

// A facet's count is what choosing it would show: the filter with the
// facet's own group set to it.
func TestProgramFacets(t *testing.T) {
	ctx := context.Background()
	s, _ := listFixture(t)
	f := ProgramFilter{BountyOnly: true, MinBounty: 5000}
	fc, err := s.ProgramFacets(ctx, f, []string{"fake", "other"})
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name      string
		got, want int
	}{
		{"total", fc.Total, 1},
		{"platform fake", fc.Platforms["fake"], 1},
		{"platform other", fc.Platforms["other"], 0},
		{"pays bounties (top 5k+ either way)", fc.Bounty, 1},
		{"VDP (top 5k+)", fc.VDP, 0},
		{"top 20k+ (paying)", fc.TopBounty[20000], 1},
		{"top 1k+ (paying)", fc.TopBounty[1000], 2},
		{"new scope 7d", fc.NewScope[FacetNewScope[0]], 1},
		{"wildcards", fc.Wildcards, 1},
		{"apps", fc.Apps, 1},
		{"APIs", fc.APIs, 0},
		{"no requirements", fc.NoRequirements, 1},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

// ReportTimes lists resolution dates for programs whose reports are listed,
// and nothing for the rest.
func TestReportTimes(t *testing.T) {
	ctx := context.Background()
	s, now := listFixture(t)
	ids := map[string]int64{}
	items, _, err := s.Programs(ctx, ProgramFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var all []int64
	for _, it := range items {
		ids[it.Handle] = it.ID
		all = append(all, it.ID)
	}
	got, err := s.ReportTimes(ctx, all, now.Add(-95*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	acme, ok := got[ids["acme"]]
	if !ok || len(acme.Resolved) != 1 || !acme.Resolved[0].Equal(now.Add(-10*24*time.Hour)) || acme.CompleteFrom != nil {
		t.Errorf("acme = %+v (listed %v)", acme, ok)
	}
	if g, ok := got[ids["globex"]]; !ok || len(g.Resolved) != 0 {
		t.Errorf("globex = %+v (listed %v), want listed with none", g, ok)
	}
	if _, ok := got[ids["initech"]]; ok {
		t.Error("initech's reports aren't listed, so it should have no entry")
	}
}

// Bounties paid all time is the platform's total, or rewarded reports times
// the average payout where only those are published (marked an estimate);
// sorting by it ranks both together, programs without either last.
func TestPaidAll(t *testing.T) {
	ctx := context.Background()
	s, _ := listFixture(t)
	f := func(v float64) *float64 { return &v }
	n := func(v int) *int { return &v }
	stats := map[string]platform.ProgramStats{
		"acme":   {Currency: "USD", BountiesPaid: f(4000)},
		"globex": {Currency: "USD", RewardedReports: n(10), AvgBountyLow: f(500), AvgBountyHigh: f(500)},
		"hooli":  {Currency: "USD", RewardedReports: n(3)}, // no average: nothing to estimate
	}
	err := s.Write(ctx, func(tx *Tx) error {
		for h, st := range stats {
			var id int64
			if err := tx.tx.QueryRowContext(ctx, `SELECT id FROM programs WHERE handle = ?`, h).Scan(&id); err != nil {
				return err
			}
			if err := tx.RecordProgramStats(ctx, id, st); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	items, _, err := s.Programs(ctx, ProgramFilter{Sort: "paidall"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		paid := "none"
		if it.PaidAll != nil {
			paid = strconv.FormatFloat(*it.PaidAll, 'f', 0, 64)
			if it.PaidAllEstimated {
				paid = "≈" + paid
			}
		}
		got = append(got, it.Handle+" "+paid)
	}
	want := []string{"globex ≈5000", "acme 4000", "hooli none", "initech none"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Where a platform publishes no paid in 90 days but lists accepted reports
// for the whole 90 days, the payable ones (not priority 5) times the average
// payout estimate it; it sorts with published figures. A listing that
// started inside the 90 days estimates nothing.
func TestPaid90Estimate(t *testing.T) {
	ctx := context.Background()
	s, now := listFixture(t)
	f := func(v float64) *float64 { return &v }
	ids := map[string]int64{}
	err := s.Write(ctx, func(tx *Tx) error {
		for _, h := range []string{"acme", "globex", "hooli"} {
			var id int64
			if err := tx.tx.QueryRowContext(ctx, `SELECT id FROM programs WHERE handle = ?`, h).Scan(&id); err != nil {
				return err
			}
			ids[h] = id
		}
		if err := tx.RecordProgramStats(ctx, ids["acme"], platform.ProgramStats{Currency: "USD", BountiesPaidLast90d: f(4000)}); err != nil {
			return err
		}
		for _, h := range []string{"globex", "hooli"} {
			if err := tx.RecordProgramStats(ctx, ids[h], platform.ProgramStats{Currency: "USD", AvgBountyLow: f(500), AvgBountyHigh: f(500)}); err != nil {
				return err
			}
		}
		listed := map[string]time.Time{"globex": now.AddDate(0, -6, 0), "hooli": now.AddDate(0, 0, -30)}
		for h, since := range listed { // as the syncer does: a page's reports, then the feed marked read
			for i, r := range []struct{ ago, priority int }{{5, 1}, {20, 3}, {30, 5}, {40, 0}, {120, 2}} {
				rep := platform.AcceptedReport{ID: h + strconv.Itoa(i), AcceptedAt: now.AddDate(0, 0, -r.ago), Priority: r.priority}
				if _, err := tx.SaveAccepted(ctx, ids[h], rep); err != nil {
					return err
				}
			}
			if err := tx.MarkAcceptedSynced(ctx, ids[h], &since); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	items, _, err := s.Programs(ctx, ProgramFilter{Sort: "paid90"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		paid := "none"
		if it.Paid90d != nil {
			paid = strconv.FormatFloat(*it.Paid90d, 'f', 0, 64)
			if it.Paid90dEstimated {
				paid = "≈" + paid + " from " + strconv.Itoa(*it.PaidAccepted90d)
			}
		}
		got = append(got, it.Handle+" "+paid)
	}
	// globex: priorities 1, 3 and none in 90 days (not the 5, not 120 days ago).
	want := []string{"acme 4000", "globex ≈1500 from 3", "hooli none", "initech none"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}

	times, err := s.ReportTimes(ctx, []int64{ids["globex"]}, now.AddDate(0, 0, -95))
	if err != nil {
		t.Fatal(err)
	}
	if g := times[ids["globex"]]; len(g.Accepted) != 4 || len(g.AcceptedPaid) != 3 {
		t.Errorf("globex accepted %d, payable %d; want 4 and 3", len(g.Accepted), len(g.AcceptedPaid))
	}
}

// Where a platform lists no reports but publishes totals of reports and of
// accepted ones, valid reports in 90 days are estimated as reports received
// in 90 days × the share accepted, and sort with counted ones.
func TestValid90Estimate(t *testing.T) {
	ctx := context.Background()
	s, _ := listFixture(t)
	n := func(v int) *int { return &v }
	stats := map[string]platform.ProgramStats{
		"hooli":   {TotalReports: n(200), AcceptedReports: n(50), ReportsLast90d: n(40)}, // 40 × ¼
		"initech": {TotalReports: n(200), ReportsLast90d: n(40)},                         // no accepted total
	}
	err := s.Write(ctx, func(tx *Tx) error {
		for h, st := range stats {
			var id int64
			if err := tx.tx.QueryRowContext(ctx, `SELECT id FROM programs WHERE handle = ?`, h).Scan(&id); err != nil {
				return err
			}
			if err := tx.RecordProgramStats(ctx, id, st); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	items, _, err := s.Programs(ctx, ProgramFilter{Sort: "resolved"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		v := "none"
		if it.Resolved90d != nil {
			v = strconv.Itoa(*it.Resolved90d)
			if it.Resolved90dEstimated {
				v = "≈" + v
			}
		}
		got = append(got, it.Handle+" "+v)
	}
	// acme's and globex's are counted from their reports.
	want := []string{"hooli ≈10", "acme 1", "globex 0", "initech none"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Money sorts order within currencies: the most common one's first, then
// each other's A to Z, each biggest first (turned around, each smallest
// first, the groups as they were); none last; ties A to Z.
func TestProgramListMoneyByCurrency(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	f := func(v float64) *float64 { return &v }
	err := s.Write(ctx, func(tx *Tx) error {
		for _, p := range []struct {
			handle, currency string
			paid             *float64
		}{
			{"usd-big", "USD", f(9000)}, {"usd-small", "USD", f(100)}, {"usd-tie-b", "USD", f(500)}, {"usd-tie-a", "USD", f(500)},
			{"eur-big", "EUR", f(50000)}, {"eur-small", "EUR", f(200)}, {"gbp", "GBP", f(70000)}, {"none", "USD", nil},
		} {
			id, err := tx.UpsertProgram(ctx, "fake", platform.Program{Handle: p.handle, Name: p.handle, URL: "https://example.com/" + p.handle,
				State: "open", OffersBounty: true, Currency: p.currency})
			if err != nil {
				return err
			}
			if err := tx.RecordProgramStats(ctx, id, platform.ProgramStats{Currency: p.currency, BountiesPaidLast90d: p.paid, BountiesPaid: p.paid}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		sort    string
		reverse bool
		want    []string
	}{
		{"paid90", false, []string{"usd-big", "usd-tie-a", "usd-tie-b", "usd-small", "eur-big", "eur-small", "gbp", "none"}},
		{"paid90", true, []string{"usd-small", "usd-tie-a", "usd-tie-b", "usd-big", "eur-small", "eur-big", "gbp", "none"}},
		{"paidall", false, []string{"usd-big", "usd-tie-a", "usd-tie-b", "usd-small", "eur-big", "eur-small", "gbp", "none"}},
	} {
		items, _, err := s.Programs(ctx, ProgramFilter{Sort: tt.sort, Reverse: tt.reverse})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, it := range items {
			got = append(got, it.Handle)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s reverse=%v: got %q, want %q", tt.sort, tt.reverse, got, tt.want)
		}
	}
}

// Ignored programs are counted for the rail even though the default
// filter leaves them out of the list.
func TestIgnoredFacet(t *testing.T) {
	ctx := context.Background()
	s, _ := listFixture(t)
	list, _, err := s.Programs(ctx, ProgramFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 {
		t.Fatal("no programs in the fixture")
	}
	p, err := s.Program(ctx, list[0].Platform, list[0].Handle)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetIgnored(ctx, p.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, f := range []ProgramFilter{{}, {Ignored: "only"}} {
		fc, err := s.ProgramFacets(ctx, f, nil)
		if err != nil {
			t.Fatal(err)
		}
		if fc.IgnoredOnly != 1 {
			t.Errorf("ignored count with %+v = %d, want 1", f.Ignored, fc.IgnoredOnly)
		}
		// With the default filter the two counts cover every program.
		if f.Ignored == "" && fc.WithIgnored != fc.Total+fc.IgnoredOnly {
			t.Errorf("with-ignored = %d, want total %d + ignored %d", fc.WithIgnored, fc.Total, fc.IgnoredOnly)
		}
	}
}
