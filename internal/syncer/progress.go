package syncer

import (
	"context"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// Progress is how far a running poll has got, for Settings to show it's
// moving: the step it's on, how many of that step's programs are done
// (Total 0: it doesn't count them), the requests it has made, and when it
// last did something (a request answered or a program done).
type Progress struct {
	Step         string
	Done, Total  int
	Requests     int64
	Started      time.Time // the run
	StepStarted  time.Time
	LastActivity time.Time
}

// progressState is a running poll's Progress and what it's worked out from.
type progressState struct {
	Progress
	p        platform.Platform
	reqsBase int64 // the platform's request count when the run started
}

// progressSample is how often a running poll's request count is read, so
// LastActivity says when it last made a request even while a step's
// count doesn't move.
const progressSample = 5 * time.Second

// trackRun starts recording runID's progress; the returned func stops it.
func (s *Syncer) trackRun(ctx context.Context, runID int64, p platform.Platform) (stop func()) {
	now := s.store.Now()
	base, _ := p.Stats()
	st := &progressState{Progress: Progress{Step: "Starting", Started: now, StepStarted: now, LastActivity: now}, p: p, reqsBase: base}
	s.progMu.Lock()
	s.progress[runID] = st
	s.progMu.Unlock()
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(progressSample)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				s.sample(runID)
			}
		}
	}()
	return func() {
		close(done)
		s.progMu.Lock()
		delete(s.progress, runID)
		s.progMu.Unlock()
	}
}

// sample reads the platform's request count; a new request is activity.
// The caller must not hold progMu.
func (s *Syncer) sample(runID int64) {
	s.progMu.Lock()
	st := s.progress[runID]
	s.progMu.Unlock()
	if st == nil {
		return
	}
	reqs, _ := st.p.Stats() // outside progMu: the platform has its own locks
	now := s.store.Now()
	s.progMu.Lock()
	if n := reqs - st.reqsBase; n != st.Requests {
		st.Requests, st.LastActivity = n, now
	}
	s.progMu.Unlock()
	// The database gets the same heartbeat, so a reader without this process's
	// memory - the doctor, a script, the next run - can tell working from stuck.
	// Best effort: a poll that cannot write its heartbeat still polls.
	_ = s.store.TouchRun(context.WithoutCancel(context.Background()), runID)
}

// step records that runID moved on to a step of total programs (0: not
// counted). Run 0 (stats refreshed outside a poll) isn't tracked.
func (s *Syncer) step(runID int64, name string, total int) {
	s.progMu.Lock()
	defer s.progMu.Unlock()
	if st := s.progress[runID]; st != nil {
		now := s.store.Now()
		st.Step, st.Done, st.Total, st.StepStarted, st.LastActivity = name, 0, total, now, now
	}
}

// advance records n more of the step's programs done.
func (s *Syncer) advance(runID int64, n int) {
	s.progMu.Lock()
	defer s.progMu.Unlock()
	if st := s.progress[runID]; st != nil {
		st.Done += n
		st.LastActivity = s.store.Now()
	}
}

// RunProgress returns how far runID, running in this process, has got;
// ok is false for a run that isn't (finished, or another process's).
func (s *Syncer) RunProgress(runID int64) (Progress, bool) {
	s.sample(runID)
	s.progMu.Lock()
	defer s.progMu.Unlock()
	st := s.progress[runID]
	if st == nil {
		return Progress{}, false
	}
	return st.Progress, true
}
