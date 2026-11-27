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

type researcherStatsFake struct {
	*fake.Platform
	calls int
	stats platform.ResearcherStats
	err   error
}

func (f *researcherStatsFake) FetchResearcherStats(context.Context) (platform.ResearcherStats, error) {
	f.calls++
	return f.stats, f.err
}

func TestSyncResearcherStats(t *testing.T) {
	e := newEnv(t)
	e.seed()
	sig1y := -2.67
	rep := 7
	f := &researcherStatsFake{
		Platform: e.fake,
		stats: platform.ResearcherStats{
			Username:   "alice_example",
			Last90Days: platform.ResearcherWindowStats{Reputation: &rep},
			PastYear:   platform.ResearcherWindowStats{Signal: &sig1y},
		},
	}

	pollWith := func(mode string) store.Run {
		e.clock = e.clock.Add(time.Minute)
		run, err := e.sync.Poll(context.Background(), f, Options{Mode: mode, Trigger: "manual"})
		if err != nil {
			t.Fatalf("Poll(%s): %v", mode, err)
		}
		return run
	}

	// Full poll fetches and saves researcher stats.
	run := pollWith(ModeFull)
	if run.Status != store.RunOK || f.calls != 1 {
		t.Fatalf("first full poll: status=%s calls=%d", run.Status, f.calls)
	}
	got, err := e.store.ResearcherStats(context.Background(), e.fake.Name())
	if err != nil || got == nil || got.PastYear.Signal == nil || *got.PastYear.Signal != -2.67 {
		t.Fatalf("stored stats = (%+v, %v)", got, err)
	}

	// Quick poll within 1h skips re-fetching researcher stats.
	run = pollWith(ModeQuick)
	if run.Status != store.RunOK || f.calls != 1 {
		t.Fatalf("fresh quick poll: status=%s calls=%d, want 1", run.Status, f.calls)
	}

	// Quick poll after 1h refreshes researcher stats.
	e.clock = e.clock.Add(61 * time.Minute)
	newSig := 2.50
	f.stats.PastYear.Signal = &newSig
	run = pollWith(ModeQuick)
	if run.Status != store.RunOK || f.calls != 2 {
		t.Fatalf("stale quick poll: status=%s calls=%d, want 2", run.Status, f.calls)
	}
	got, err = e.store.ResearcherStats(context.Background(), e.fake.Name())
	if err != nil || got == nil || got.PastYear.Signal == nil || *got.PastYear.Signal != 2.50 {
		t.Fatalf("updated stats = (%+v, %v)", got, err)
	}

	// ErrNoWebLogin and transient errors are non-fatal (run still succeeds).
	f.err = platform.ErrNoWebLogin
	run = pollWith(ModeFull)
	if run.Status != store.RunOK || run.Error != "" {
		t.Fatalf("ErrNoWebLogin should be non-fatal: %+v", run)
	}
	f.err = errors.New("temporary graphql error")
	run = pollWith(ModeFull)
	if run.Status != store.RunOK || run.Error != "" {
		t.Fatalf("transient error should leave run OK: %+v", run)
	}
}
