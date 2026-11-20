// Package scheduler runs polls on their intervals while the server is up.
package scheduler

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
	"github.com/mdpsec/huntdeck/internal/syncer"
)

// Job polls one platform in one mode, as often as the Schedule says.
type Job struct {
	Platform platform.Platform
	Mode     string
	// Concurrency is the number of parallel scope fetches.
	Concurrency int
}

// firstDelay is used when a platform+mode has never run. A short delay for
// the full poll and a full interval for the quick poll means a fresh install
// does one full import instead of two overlapping ones.
func (j Job) firstDelay(interval time.Duration) time.Duration {
	switch j.Mode {
	case syncer.ModeFull:
		return 5 * time.Second
	case syncer.ModeWeb:
		// After the first full poll has listed the programs.
		return 10 * time.Minute
	}
	return interval
}

// busyRetry is how long to wait when another poll holds the lease.
// restartedRetry is how soon a run a restart cut short is tried again: short
// enough that an interrupted full poll finishes before the next restart,
// long enough not to spin.
const restartedRetry = 5 * time.Minute

const busyRetry = time.Minute

// loginRetryFirst is how soon a browser sync whose login failed (the site
// took Chrome for a bot, or the login page broke) runs again. Each failure
// in a row doubles it, until it reaches the sync's own interval: soon
// enough not to lose a day, spaced out so a run of tries doesn't look
// more like a bot. Refused credentials aren't retried (account lockout).
const loginRetryFirst = time.Hour

// loginRetry is how soon to sync again after fails browser logins in a
// row, and false once that's no sooner than interval.
func loginRetry(fails int, interval time.Duration) (time.Duration, bool) {
	if fails < 1 {
		return 0, false
	}
	d := loginRetryFirst
	for range fails - 1 {
		if d >= interval {
			break
		}
		d *= 2
	}
	return d, d < interval
}

// loginFailed reports whether j is a browser sync whose last login failed
// in a way worth trying again soon.
func loginFailed(j Job) bool {
	ws, ok := j.Platform.(platform.WebSyncer)
	if !ok || j.Mode != syncer.ModeWeb {
		return false
	}
	s := ws.WebSession()
	return !s.SignedIn && !s.Stuck && s.Failure != ""
}

// keepAliveEvery is how often idle login sessions are touched.
const keepAliveEvery = 5 * time.Minute

// Run starts every job and blocks until ctx is cancelled and all in-flight
// polls have stopped. Intervals and enabled platforms come from sched and
// may change while running.
func Run(ctx context.Context, log *slog.Logger, st *store.Store, sy *syncer.Syncer, sched *Schedule, jobs []Job) {
	var wg sync.WaitGroup
	alive := map[platform.Platform]bool{}
	for _, j := range jobs {
		name := j.Platform.Name()
		if ka, ok := j.Platform.(platform.KeepAliver); ok && !alive[j.Platform] {
			alive[j.Platform] = true
			wg.Add(1)
			go func() {
				defer wg.Done()
				keepAlive(ctx, log.With("platform", name), ka, keepAliveEvery, func() bool { return sched.Enabled(name) })
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			runJob(ctx, log.With("platform", name, "mode", j.Mode), st, sy, sched, j)
		}()
	}
	wg.Wait()
}

func runJob(ctx context.Context, log *slog.Logger, st *store.Store, sy *syncer.Syncer, sched *Schedule, j Job) {
	name := j.Platform.Name()
	var next time.Time        // zero: work it out from the last run
	var scheduledAt time.Time // when next was worked out
	var loginFails int        // browser logins failed in a row
	// soon is when to run again after a run at at: sooner than interval
	// while a browser sync's login keeps failing.
	soon := func(at time.Time, interval time.Duration) (time.Time, bool) {
		if !loginFailed(j) {
			loginFails = 0
			return time.Time{}, false
		}
		loginFails++
		d, ok := loginRetry(loginFails, interval)
		if !ok {
			return time.Time{}, false
		}
		log.Warn("browser login failed; trying again sooner", "next", at.Add(d).Format(time.RFC3339), "failures_in_a_row", loginFails)
		return at.Add(d), true
	}
	for {
		changed := sched.Changed()
		interval := sched.Get(name).Interval(j.Mode)
		if j.Mode == syncer.ModeWeb {
			interval = sched.WebInterval(name)
		}
		if interval <= 0 {
			log.Info("scheduled polls off", "platform_enabled", sched.Enabled(name))
			select {
			case <-ctx.Done():
				return
			case <-changed:
				next = time.Time{}
				continue
			}
		}
		if next.IsZero() {
			next, scheduledAt = firstRun(ctx, log, st, name, j.Mode, interval, j.firstDelay(interval), time.Now()), time.Now()
			log.Info("poll scheduled", "next", next.Format(time.RFC3339), "interval", interval)
		}
		t := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-changed:
			t.Stop()
			next = time.Time{}
			continue
		case <-t.C:
		}
		// A manual run since this was scheduled moves the next one back, so
		// "run now" isn't followed by the same run minutes later.
		if due, later := ranSince(ctx, st, name, j.Mode, interval, scheduledAt); later {
			next, scheduledAt = due, time.Now()
			if retry, ok := soon(due.Add(-interval), interval); ok { // from the manual run
				next = retry
			}
			log.Info("poll rescheduled after a manual run", "next", next.Format(time.RFC3339))
			continue
		}

		// A poll that hangs holds its platform's lease until the row looks
		// abandoned (two hours). Give it a limit instead: past it, the run fails
		// with a reason of its own and the platform is free again.
		pollCtx, cancelPoll := context.WithTimeoutCause(ctx, syncer.PollDeadline(j.Mode), syncer.ErrPollTimeout)
		_, err := sy.Poll(pollCtx, j.Platform, syncer.Options{Mode: j.Mode, Trigger: "schedule", Concurrency: j.Concurrency})
		cancelPoll()
		switch {
		case ctx.Err() != nil:
			return
		case syncer.IsRunInProgress(err):
			log.Info("another poll is running; retrying shortly")
			next = time.Now().Add(busyRetry)
			continue
		case err != nil:
			log.Error("poll could not start", "err", err)
		}
		// (A retry after another poll keeps scheduledAt, so a manual run that
		// held the lease still counts.)
		next, scheduledAt = time.Now().Add(jitter(interval)), time.Now()
		if retry, ok := soon(time.Now(), interval); ok {
			next = retry
		}
	}
}

// lastRunner is the part of the store firstRun needs.
type lastRunner interface {
	LastRun(ctx context.Context, platform, mode string) (*store.Run, error)
}

// firstRun picks when a job first runs after startup, so restarts keep the
// schedule instead of resetting it: one interval after the job's last run,
// or soon if that is overdue. A full poll does everything a quick poll does,
// so quick jobs also count the last full poll.
func firstRun(ctx context.Context, log *slog.Logger, st lastRunner, platformName, mode string,
	interval, firstDelay time.Duration, now time.Time) time.Time {
	last, err := lastStart(ctx, st, platformName, mode)
	if err != nil {
		log.Warn("cannot read last poll; scheduling from now", "err", err)
		return now.Add(firstDelay)
	}
	if last == nil {
		return now.Add(firstDelay)
	}
	// A run that a restart cut short is retried soon, not a whole interval
	// later. This deployment restarts often, and a full poll of HackerOne takes
	// longer than the gap between two restarts: with the plain interval, every
	// restart pushed the next attempt six hours out and a full poll never
	// finished. Interrupted says "we stopped it", not "the platform refused", so
	// retrying costs nothing; a run that FAILED keeps the interval, or an auth
	// error turns into a poll every few minutes.
	if r, err := st.LastRun(ctx, platformName, mode); err == nil && r != nil {
		switch {
		case mode == syncer.ModeWeb && r.Status != store.RunOK:
			if d, ok := loginRetry(1, interval); ok {
				interval = d
			}
		case r.Status == store.RunFailed && r.Error == store.RunInterrupted:
			if interval > restartedRetry {
				interval = restartedRetry
			}
		}
	}
	due := last.Add(interval)
	if due.Before(now) {
		return now.Add(5 * time.Second)
	}
	return due
}

// lastStart is when the job's last run started, counting full polls for
// quick jobs; nil if it never ran.
func lastStart(ctx context.Context, st lastRunner, platformName, mode string) (*time.Time, error) {
	modes := []string{mode}
	if mode == syncer.ModeQuick {
		modes = append(modes, syncer.ModeFull)
	}
	var last *time.Time
	for _, m := range modes {
		r, err := st.LastRun(ctx, platformName, m)
		if err != nil {
			return nil, err
		}
		if r != nil && (last == nil || r.StartedAt.After(*last)) {
			last = &r.StartedAt
		}
	}
	return last, nil
}

// ranSince reports whether a run started after the job was scheduled at
// scheduledAt (a manual one), and if so when the job is due instead: one
// interval after it. A store error lets the job run.
func ranSince(ctx context.Context, st lastRunner, platformName, mode string,
	interval time.Duration, scheduledAt time.Time) (time.Time, bool) {
	last, err := lastStart(ctx, st, platformName, mode)
	if err != nil || last == nil || !last.After(scheduledAt) {
		return time.Time{}, false
	}
	return last.Add(interval), true
}

// jitter spreads polls by ±5% so many instances don't hit a platform in sync.
func jitter(d time.Duration) time.Duration {
	f := 0.95 + 0.1*rand.Float64() //nolint:gosec // scheduling jitter needs no crypto randomness
	return time.Duration(float64(d) * f)
}

// keepAlive touches a platform's session periodically while the platform is
// enabled, logging only when its health changes so a dead session doesn't
// flood the log.
func keepAlive(ctx context.Context, log *slog.Logger, ka platform.KeepAliver, every time.Duration, enabled func() bool) {
	t := time.NewTicker(every)
	defer t.Stop()
	healthy := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !enabled() {
			continue
		}
		err := ka.KeepAlive(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil && healthy:
			log.Warn("session keepalive failed", "err", err)
		case err == nil && !healthy:
			log.Info("session keepalive working again")
		}
		healthy = err == nil
	}
}
