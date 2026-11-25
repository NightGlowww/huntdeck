package syncer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/fake"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Polls read the user's reports: details once, then only for changed ones;
// a second identical poll changes nothing; a report missing from a complete
// listing is kept and marked gone; a failure doesn't make the poll partial.
func TestPollReadsReports(t *testing.T) {
	e := newEnv(t)
	e.seed()
	rp := &fake.Reporting{Platform: e.fake}
	day := func(d int) time.Time { return time.Date(2026, 3, d, 0, 0, 0, 0, time.UTC) }
	rp.Reports = []platform.Report{
		{Key: "1", ProgramHandle: "acme", Title: "XSS", State: platform.ReportResolved, SubmittedAt: day(1), Marker: "a",
			Payments: []platform.ReportPayment{{Kind: "bounty", Amount: 100, Currency: "USD", AwardedAt: day(3)}}},
		{Key: "2", ProgramHandle: "globex", Title: "IDOR", State: platform.ReportNew, SubmittedAt: day(2), Marker: "b"},
	}
	ctx := context.Background()
	poll := func(mode string) store.Run {
		t.Helper()
		e.clock = e.clock.Add(time.Minute)
		run, err := e.sync.Poll(ctx, rp, Options{Mode: mode, Trigger: "manual"})
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	if run := poll(ModeFull); run.Status != store.RunOK || len(rp.Detailed) != 2 {
		t.Fatalf("first poll %s, details %v", run.Status, rp.Detailed)
	}
	rs, err := e.store.MyReports(ctx, store.ReportFilter{Platform: "fake"})
	if err != nil || len(rs) != 2 || rs[1].ProgramID == nil || len(rs[1].Payments) != 1 {
		t.Fatalf("stored: %+v %v", rs, err)
	}
	if run := poll(ModeFull); run.Status != store.RunOK || len(rp.Detailed) != 0 || run.Changes != 0 {
		t.Errorf("identical second poll: %s, details %v, changes %d", run.Status, rp.Detailed, run.Changes)
	}

	// A quick poll within the hour doesn't ask.
	asks := rp.Asks
	poll(ModeQuick)
	if rp.Asks != asks {
		t.Errorf("quick poll within the hour read reports")
	}

	// Report 2 is gone from a complete listing: kept, marked gone. A
	// failure is reported without failing the poll.
	rp.Reports = rp.Reports[:1]
	rp.Err = errors.New("boom")
	if run := poll(ModeFull); run.Status != store.RunOK || run.Error == "" {
		t.Errorf("failed report sync: %s %q", run.Status, run.Error)
	}
	rs, _ = e.store.MyReports(ctx, store.ReportFilter{Platform: "fake"})
	if len(rs) != 2 || rs[0].GoneAt == nil {
		t.Errorf("gone report: %+v", rs)
	}

	// Turned off in Settings: not read.
	if err := e.store.SetReportsEnabled(ctx, "fake", false); err != nil {
		t.Fatal(err)
	}
	asks = rp.Asks
	poll(ModeFull)
	if rp.Asks != asks {
		t.Errorf("read reports while turned off")
	}
}
