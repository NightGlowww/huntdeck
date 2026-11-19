package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/db"
)

// A temporary harness: times the queries a program page runs, against a
// copy of a real database (HUNTDECK_PERF_DB). Skipped without it.
func TestPerfProgramPage(t *testing.T) {
	path := os.Getenv("HUNTDECK_PERF_DB")
	if path == "" {
		t.Skip("set HUNTDECK_PERF_DB to a database copy")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	s := New(d, time.Now)

	var id int64
	if err := d.R.QueryRowContext(ctx, "SELECT id FROM programs WHERE platform = 'hackerone' AND handle = '1win_com'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	step := func(name string, fn func() error) {
		t0 := time.Now()
		if err := fn(); err != nil {
			t.Logf("%-22s FAILED %v", name, err)
			return
		}
		t.Logf("%-22s %6.1f ms", name, float64(time.Since(t0).Microseconds())/1000)
	}

	step("Program", func() error { _, err := s.Program(ctx, "hackerone", "1win_com"); return err })
	step("Programs(ID)", func() error { _, _, err := s.Programs(ctx, ProgramFilter{ID: id}); return err })
	step("HuntHistory", func() error { _, err := s.HuntHistory(ctx, id, now.Add(-90*24*time.Hour)); return err })
	step("ProgramDisclosures", func() error { _, _, err := s.ProgramDisclosures(ctx, id, 10); return err })
	step("ProgramStatsHistory", func() error { _, err := s.ProgramStatsHistory(ctx, id); return err })
	step("ProgramResolutions", func() error { _, err := s.ProgramResolutions(ctx, id, now, 6); return err })
	step("ProgramAccepted", func() error { _, err := s.ProgramAccepted(ctx, id, now, 6); return err })
	step("Changes(program)", func() error {
		_, _, err := s.Changes(ctx, ChangeFilter{ProgramID: id, Newest: true, CollapseFlips: true, Limit: 20})
		return err
	})
}

// The Stats page's two big queries.
func TestPerfStatsPage(t *testing.T) {
	path := os.Getenv("HUNTDECK_PERF_DB")
	if path == "" {
		t.Skip("set HUNTDECK_PERF_DB")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	s := New(d, time.Now)
	step := func(name string, fn func() error) {
		t0 := time.Now()
		if err := fn(); err != nil {
			t.Logf("%-24s FAILED %v", name, err)
			return
		}
		t.Logf("%-24s %6.1f ms", name, float64(time.Since(t0).Microseconds())/1000)
	}
	step("ProgramStatsList", func() error {
		_, _, err := s.ProgramStatsList(ctx, ProgramFilter{Limit: 100})
		return err
	})
	step("Programs(all)", func() error {
		_, _, err := s.Programs(ctx, ProgramFilter{Limit: 1 << 20})
		return err
	})
	step("Programs(page)", func() error {
		_, _, err := s.Programs(ctx, ProgramFilter{Limit: 100})
		return err
	})
}

// Does the stats list cost come from the page size or from the whole set?
func TestPerfStatsListScaling(t *testing.T) {
	path := os.Getenv("HUNTDECK_PERF_DB")
	if path == "" {
		t.Skip("set HUNTDECK_PERF_DB")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	s := New(d, time.Now)
	for _, n := range []int{10, 100, 1000} {
		t0 := time.Now()
		if _, _, err := s.ProgramStatsList(ctx, ProgramFilter{Limit: n}); err != nil {
			t.Fatal(err)
		}
		t.Logf("ProgramStatsList(limit %4d) %6.1f ms", n, float64(time.Since(t0).Microseconds())/1000)
	}
}

// The dashboard's own queries.
func TestPerfBoard(t *testing.T) {
	path := os.Getenv("HUNTDECK_PERF_DB")
	if path == "" {
		t.Skip("set HUNTDECK_PERF_DB")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	s := New(d, time.Now)
	for i := 0; i < 2; i++ {
		t0 := time.Now()
		if _, err := s.PlatformsChecked(ctx); err != nil {
			t.Fatal(err)
		}
		t.Logf("PlatformsChecked   %6.1f ms", float64(time.Since(t0).Microseconds())/1000)
		t0 = time.Now()
		if _, err := s.Summary(ctx); err != nil {
			t.Fatal(err)
		}
		t.Logf("Summary            %6.1f ms", float64(time.Since(t0).Microseconds())/1000)
		t0 = time.Now()
		if _, err := s.TopHunts(ctx, 6); err != nil {
			t.Fatal(err)
		}
		t.Logf("TopHunts           %6.1f ms", float64(time.Since(t0).Microseconds())/1000)
	}
}

// The dashboard's fresh attack surface.
func TestPerfBoardTargets(t *testing.T) {
	path := os.Getenv("HUNTDECK_PERF_DB")
	if path == "" {
		t.Skip("set HUNTDECK_PERF_DB")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	s := New(d, time.Now)
	for i := 0; i < 2; i++ {
		t0 := time.Now()
		_, _, err := s.BestTargets(ctx, TargetFilter{IncludePaused: true}, "", 25, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("BestTargets   %6.1f ms", float64(time.Since(t0).Microseconds())/1000)
	}
}

// The Targets page's rows.
func TestPerfTargetRows(t *testing.T) {
	path := os.Getenv("HUNTDECK_PERF_DB")
	if path == "" {
		t.Skip("set HUNTDECK_PERF_DB")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	s := New(d, time.Now)
	for i := 0; i < 3; i++ {
		t0 := time.Now()
		_, _, err := s.TargetRows(ctx, TargetFilter{Kind: "wildcard", IncludePaused: true}, "", 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("TargetRows(wildcard) %6.1f ms", float64(time.Since(t0).Microseconds())/1000)
	}
}

// The Targets page's exact filter.
func TestPerfTargetRowsPage(t *testing.T) {
	path := os.Getenv("HUNTDECK_PERF_DB")
	if path == "" {
		t.Skip("set HUNTDECK_PERF_DB")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	s := New(d, time.Now)
	for _, kind := range []string{"wildcard", "domain", "url"} {
		for i := 0; i < 2; i++ {
			t0 := time.Now()
			_, _, err := s.TargetRows(ctx, TargetFilter{Kind: kind, Order: TargetsByNewest}, "", 100, 0)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("TargetRows(%s) %6.1f ms", kind, float64(time.Since(t0).Microseconds())/1000)
		}
	}
}

// Prints the SQL the Targets page runs for a filter, for EXPLAIN QUERY PLAN.
func TestPerfTargetRowsSQL(t *testing.T) {
	path := os.Getenv("HUNTDECK_PERF_DB")
	if path == "" {
		t.Skip("set HUNTDECK_PERF_DB")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	s := New(d, time.Now)
	_ = s
	f := TargetFilter{Kind: "domain", Order: TargetsByNewest}
	k := kindOf(f.Kind)
	conds, args := targetConds(f, k, "")
	_ = args
	t.Logf("conds: %v", conds)
	t.Logf("value expr: %s | same: %s | ordered: %s", k.value, k.same("t2"), k.ordered)
}

// The Programs page's list query.
func TestPerfProgramListPage(t *testing.T) {
	path := os.Getenv("HUNTDECK_PERF_DB")
	if path == "" {
		t.Skip("set HUNTDECK_PERF_DB")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	s := New(d, time.Now)
	for i := 0; i < 3; i++ {
		t0 := time.Now()
		if _, _, err := s.Programs(ctx, ProgramFilter{State: "open", Sort: "name", Limit: 30}); err != nil {
			t.Fatal(err)
		}
		t.Logf("Programs(name,30) %6.1f ms", float64(time.Since(t0).Microseconds())/1000)
	}
}

// The Programs page's default order.
func TestPerfProgramListScore(t *testing.T) {
	path := os.Getenv("HUNTDECK_PERF_DB")
	if path == "" {
		t.Skip("set HUNTDECK_PERF_DB")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	s := New(d, time.Now)
	for i := 0; i < 3; i++ {
		t0 := time.Now()
		if _, _, err := s.Programs(ctx, ProgramFilter{State: "open", Sort: "huntscore", Limit: 30}); err != nil {
			t.Fatal(err)
		}
		t.Logf("Programs(huntscore,30) %6.1f ms", float64(time.Since(t0).Microseconds())/1000)
	}
}

// The Disclosed page's two reads.
func TestPerfDisclosed(t *testing.T) {
	path := os.Getenv("HUNTDECK_PERF_DB")
	if path == "" {
		t.Skip("set HUNTDECK_PERF_DB")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	s := New(d, time.Now)
	f := DisclosureFilter{Limit: 100}
	for i := 0; i < 2; i++ {
		t0 := time.Now()
		if _, _, err := s.Disclosures(ctx, f); err != nil {
			t.Fatal(err)
		}
		t.Logf("Disclosures(100)   %6.1f ms", float64(time.Since(t0).Microseconds())/1000)
	}
}

// Deep pages: what an OFFSET costs on the Disclosed list.
func TestPerfDisclosedOffset(t *testing.T) {
	path := os.Getenv("HUNTDECK_PERF_DB")
	if path == "" {
		t.Skip("set HUNTDECK_PERF_DB")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	s := New(d, time.Now)
	for _, off := range []int{0, 1000, 4000, 8000} {
		t0 := time.Now()
		if _, _, err := s.Disclosures(ctx, DisclosureFilter{Limit: 100, Offset: off}); err != nil {
			t.Fatal(err)
		}
		t.Logf("Disclosures(offset %5d) %6.1f ms", off, float64(time.Since(t0).Microseconds())/1000)
	}
}

// Deep pages on the Targets list: does the cost grow with the offset?
func TestPerfTargetsOffset(t *testing.T) {
	path := os.Getenv("HUNTDECK_PERF_DB")
	if path == "" {
		t.Skip("set HUNTDECK_PERF_DB")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	s := New(d, time.Now)
	for _, off := range []int{0, 1000, 2000, 4400} {
		t0 := time.Now()
		if _, _, err := s.TargetRows(ctx, TargetFilter{Kind: "domain", Order: TargetsByNewest}, "", 100, off); err != nil {
			t.Fatal(err)
		}
		t.Logf("TargetRows(domain, offset %4d) %6.1f ms", off, float64(time.Since(t0).Microseconds())/1000)
	}
}

// The reads added or changed while the pages were being made cheaper: the
// one-pass snapshot read, the board's week and the Targets counts.
func TestPerfFanOut(t *testing.T) {
	path := os.Getenv("HUNTDECK_PERF_DB")
	if path == "" {
		t.Skip("set HUNTDECK_PERF_DB")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	s := New(d, time.Now)
	now := time.Now()
	week := now.Add(-7 * 24 * time.Hour)
	step := func(name string, fn func() error) {
		var last float64
		for i := 0; i < 2; i++ {
			t0 := time.Now()
			if err := fn(); err != nil {
				t.Logf("%-26s FAILED %v", name, err)
				return
			}
			last = float64(time.Since(t0).Microseconds()) / 1000
		}
		t.Logf("%-26s %6.1f ms (warm)", name, last)
	}
	step("SnapshotsFor(30)", func() error {
		ids := make([]int64, 0, 30)
		for i := int64(1); i <= 30; i++ {
			ids = append(ids, i)
		}
		_, err := s.SnapshotsFor(ctx, ids, now.Add(-180*24*time.Hour))
		return err
	})
	step("BoardChanges(all, 7d)", func() error {
		_, err := s.BoardChanges(ctx, week, false, 2000)
		return err
	})
	step("BoardChanges(states, 7d)", func() error {
		_, err := s.BoardChanges(ctx, week, true, 2000)
		return err
	})
	step("BoardCounts(7d)", func() error {
		_, err := s.BoardCounts(ctx, week)
		return err
	})
	step("TargetFacetsFor(domain)", func() error {
		_, err := s.TargetFacetsFor(ctx, TargetFilter{Kind: "domain"}, "", []time.Time{week})
		return err
	})
}
