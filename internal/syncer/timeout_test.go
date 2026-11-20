package syncer

import (
	"context"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/fake"
	"github.com/mdpsec/huntdeck/internal/store"
)

// stallsUntilDeadline takes the poll past its deadline the way a hung request
// does: it waits for the context, then reports the cause.
type stallsUntilDeadline struct{ *fake.Platform }

func (s stallsUntilDeadline) FetchScope(ctx context.Context, _ platform.Program) ([]platform.ScopeEntry, error) {
	<-ctx.Done()
	return nil, context.Cause(ctx)
}

// A poll that ran past its deadline is not an interruption. The difference
// matters: an interrupted run is retried in five minutes, so a run that hangs
// would hang again in five minutes, forever.
func TestPollThatTimesOutFailsInsteadOfInterrupting(t *testing.T) {
	e := newEnv(t)
	e.seed()
	ctx, cancel := context.WithTimeoutCause(context.Background(), 30*time.Millisecond, ErrPollTimeout)
	defer cancel()
	run, err := e.sync.Poll(ctx, stallsUntilDeadline{e.fake}, Options{Mode: ModeFull, Trigger: "schedule"})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if run.Status != store.RunFailed || run.Error != ErrPollTimeout.Error() {
		t.Fatalf("run = %+v, want failed with %q", run, ErrPollTimeout)
	}
	if run.DisplayStatus() == store.RunInterrupted {
		t.Fatal("a timed-out poll is shown as interrupted, so it would retry in minutes")
	}
}

// And the deadlines themselves are bounded, with a full poll given more room.
func TestPollDeadlinesAreSane(t *testing.T) {
	for _, c := range []struct {
		mode string
		max  time.Duration
	}{
		{ModeFull, time.Hour}, {ModeQuick, 30 * time.Minute}, {ModeWeb, time.Hour},
	} {
		if d := PollDeadline(c.mode); d <= 0 || d > c.max {
			t.Errorf("PollDeadline(%s) = %v, want (0, %v]", c.mode, d, c.max)
		}
	}
	if PollDeadline(ModeFull) <= PollDeadline(ModeQuick) {
		t.Error("a full poll is given no more time than a quick one")
	}
}
