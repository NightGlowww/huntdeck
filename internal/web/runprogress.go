package web

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/store"
)

// RunProgress is how far a running poll has got (syncer.Progress): the
// step it's on, how many of the step's programs are done (Total 0: not
// counted), its requests, and when it last did something.
type RunProgress struct {
	Step         string
	Done, Total  int
	Requests     int64
	Started      time.Time
	StepStarted  time.Time
	LastActivity time.Time
}

// stalledAfter is how long without a request or a program done before a
// running poll is shown as possibly stuck.
const stalledAfter = 3 * time.Minute

// liveRun is a running poll as Settings shows it.
type liveRun struct {
	Step     string // "Program stats 519 / 643"
	Left     string // "about 10m left", "" when it can't tell
	Usually  string // "usually 11m–47m", from the latest finished runs
	Requests int64
	Idle     string // "3s", since it last did something
	Stalled  bool   // nothing for stalledAfter
	Known    bool   // progress from this process (else only the time running)
	Running  string // "58m"
}

// liveRuns describes the running runs among runs, by ID.
func (s *server) liveRuns(ctx context.Context, runs []store.Run) map[int64]liveRun {
	out := map[int64]liveRun{}
	now := time.Now()
	for _, r := range runs {
		if r.Status != store.RunRunning {
			continue
		}
		l := liveRun{Running: humanize.Duration(max(now.Sub(r.StartedAt), time.Second))}
		if ds, err := s.Store.RecentDurations(ctx, r.Platform, r.Mode, r.Trigger == store.TriggerProgram, 5); err == nil && len(ds) > 0 {
			lo, hi := slices.Min(ds), slices.Max(ds)
			l.Usually = "usually " + humanize.Duration(max(lo, time.Second))
			if hi-lo > lo/5 {
				l.Usually += "–" + humanize.Duration(hi)
			}
		}
		// The heartbeat is in the row, so a run started by a process that has since
		// restarted still shows whether it is working - its in-memory progress is
		// gone. Without this, a row left behind by a dead process reads as a live
		// poll with nothing to say.
		if r.LastActivity != nil && !r.LastActivity.IsZero() {
			idle := max(now.Sub(*r.LastActivity), 0)
			l.Idle, l.Stalled = humanize.Duration(max(idle, time.Second)), idle >= stalledAfter
		}
		if s.Progress != nil {
			if p, ok := s.Progress(r.ID); ok {
				l.Known, l.Requests = true, p.Requests
				l.Step = p.Step
				if p.Total > 0 {
					l.Step += " " + strconv.Itoa(min(p.Done, p.Total)) + " / " + strconv.Itoa(p.Total)
					if p.Done > 0 && p.Done < p.Total {
						per := now.Sub(p.StepStarted) / time.Duration(p.Done)
						l.Left = "about " + humanize.Duration(max(per*time.Duration(p.Total-p.Done), time.Minute)) + " left in this step"
					}
				}
				idle := max(now.Sub(p.LastActivity), 0)
				l.Idle, l.Stalled = humanize.Duration(max(idle, time.Second)), idle >= stalledAfter
			}
		}
		out[r.ID] = l
	}
	return out
}
