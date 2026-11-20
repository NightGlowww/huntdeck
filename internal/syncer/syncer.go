// Package syncer runs polls: it asks a platform for programs and scope,
// stores the result, and records what changed.
//
// Removal rule: a program is only marked removed when the program listing was
// complete, and a scope entry only when that program's scope fetch succeeded.
// Partial data can add and update, never remove.
//
// Mass-removal guard: a "complete" listing that would remove most known
// programs, or an empty scope for a program that had some, is far more
// likely a platform bug or format change than reality, so nothing is
// removed and the run is marked partial. --force-removals overrides the
// program check for the rare real case.
package syncer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/huntscore"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Poll modes.
const (
	// ModeFull lists programs and refreshes every program's scope, except
	// ones the platform says haven't changed since the last download.
	ModeFull = "full"
	// ModeQuick lists programs and fetches scope only for programs that
	// have never been synced (new programs and new invites) and ones the
	// listing says have changed.
	ModeQuick = "quick"
	// ModeWeb reads program stats from a platform's logged-in website
	// (platform.WebSyncer): the browser sync, run on its own schedule.
	ModeWeb = "web"
)

// scopeRecheck is how long polls trust the platform's word that a scope
// hasn't changed. After it, a full poll downloads the scope anyway, in case
// the platform changed it without saying. It was a day until 2026-10-28,
// but a week of daily re-downloads (about 1,100 requests a day, 900 of
// them Bugcrowd's) found nothing the platforms' own change signals had
// missed.
const scopeRecheck = 7 * 24 * time.Hour

// changeSlack allows for clock differences with the platform: a change
// reported up to this long before the last download may not be in it.
const changeSlack = 10 * time.Minute

// massRemovalMin is the smallest number of program removals the guard
// questions; below it, removals of over half the programs are allowed so tiny
// accounts aren't stuck.
const massRemovalMin = 10

// statsMaxAge is how old a program's public stats may get before a full poll
// refreshes them. Full polls run every few hours, so stats refresh about daily.
const statsMaxAge = 20 * time.Hour

// completeListingEvery is how often quick polls of a platform that can list
// its newest programs (platform.RecentLister) still read the complete
// listing, which is what finds private invites and removed programs. In
// between they read only the newest page.
const completeListingEvery = 2 * time.Hour

// staleRunAfter is how long a "running" row may sit before it's treated as
// abandoned by a crashed process.
const staleRunAfter = 2 * time.Hour

// Syncer runs polls against the store.
type Syncer struct {
	store       *store.Store
	log         *slog.Logger
	concurrency int

	// progress is each running poll's, by run ID (progress.go).
	progMu   sync.Mutex
	progress map[int64]*progressState
}

// New returns a Syncer. concurrency bounds parallel scope fetches.
func New(st *store.Store, log *slog.Logger, concurrency int) *Syncer {
	return &Syncer{store: st, log: log, concurrency: max(1, concurrency), progress: map[int64]*progressState{}}
}

// Options controls one poll.
type Options struct {
	Mode    string // ModeFull or ModeQuick
	Trigger string // "schedule" or "manual"
	// Limit, if > 0, processes only the first Limit programs from the
	// listing. The run then counts as incomplete, so nothing is removed.
	Limit int
	// Concurrency overrides the Syncer's default parallel scope fetches.
	Concurrency int
	// ForceRemovals applies program removals even when the mass-removal
	// guard would hold them back.
	ForceRemovals bool
	// RefetchScope downloads every listed program's scope in a full poll,
	// even those the platform says are unchanged: to pick up what a newer
	// HuntDeck reads from a download without waiting for scopeRecheck.
	RefetchScope bool
	// Handles, if set, keeps only these programs of the listing: a refresh
	// of them (see RefreshPrograms). The run counts as incomplete, so
	// nothing is removed, and the platform-wide feeds (histories,
	// disclosures, resolutions, accepted reports) are left to the polls.
	Handles []string
}

// Poll runs one poll of p and returns the finished run record. It returns
// store.ErrRunInProgress if another poll of p is running.
func (s *Syncer) Poll(ctx context.Context, p platform.Platform, o Options) (store.Run, error) {
	if o.Mode == ModeWeb {
		return s.WebSync(ctx, p, o.Trigger)
	}
	if o.Mode != ModeFull && o.Mode != ModeQuick {
		return store.Run{}, fmt.Errorf("unknown poll mode %q", o.Mode)
	}
	runID, err := s.store.StartRun(ctx, p.Name(), o.Mode, o.Trigger, staleRunAfter)
	if err != nil {
		return store.Run{}, err
	}
	log := s.log.With("platform", p.Name(), "mode", o.Mode, "run", runID)
	log.Info("poll started")
	reqs0, limited0 := p.Stats()

	run := store.Run{ID: runID, Platform: p.Name(), Mode: o.Mode, Trigger: o.Trigger}
	stopTracking := s.trackRun(ctx, runID, p)
	runErr, statsErr := s.poll(ctx, log, p, o, &run)
	stopTracking()

	reqs1, limited1 := p.Stats()
	run.Requests, run.RateLimited = reqs1-reqs0, limited1-limited0
	switch {
	case ctx.Err() != nil && errors.Is(context.Cause(ctx), ErrPollTimeout):
		// Its own outcome, not an interruption: a run that hung must not be
		// retried in minutes, or it hangs again in minutes.
		run.Status, run.Error = store.RunFailed, ErrPollTimeout.Error()
	case ctx.Err() != nil:
		run.Status, run.Error = store.RunFailed, store.RunInterrupted
	case runErr != nil && run.ProgramsSeen == 0:
		run.Status, run.Error = store.RunFailed, runErr.Error()
	case runErr != nil || run.ScopesFailed > 0:
		run.Status = store.RunPartial
		if runErr != nil {
			run.Error = runErr.Error()
		} else {
			run.Error = fmt.Sprintf("%d scope fetches failed", run.ScopesFailed)
		}
	default:
		run.Status = store.RunOK
	}
	// Stats come from a separate, often unofficial, endpoint: a failure
	// there is reported but doesn't make the scope poll partial.
	if statsErr != nil && run.Error == "" {
		run.Error = statsErr.Error()
	}
	if n, err := s.store.CountChanges(context.WithoutCancel(ctx), runID); err == nil {
		run.Changes = n
	}
	// Record the outcome even if ctx was cancelled, or the lease stays held.
	if err := s.store.FinishRun(context.WithoutCancel(ctx), run); err != nil {
		return run, fmt.Errorf("record poll result: %w", err)
	}
	// The log is at its largest here and nothing else is writing: fold it back so
	// the file does not keep the high-water mark of this poll. Bounded, because a
	// TRUNCATE checkpoint waits for readers and WithoutCancel gives it no deadline:
	// unbounded, it held the database and every later read waited behind it - the
	// board stopped answering while the server still looked healthy.
	ckCtx, cancelCk := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	if err := s.store.CheckpointWAL(ckCtx); err != nil {
		log.Debug("wal checkpoint skipped", "err", err)
	}
	cancelCk()
	log.Info("poll finished", "status", run.Status, "programs", run.ProgramsSeen, "scopes_fetched", run.ScopesFetched,
		"scopes_failed", run.ScopesFailed, "stats_fetched", run.StatsFetched, "stats_failed", run.StatsFailed, "disclosures_new", run.DisclosuresNew, "resolutions_new", run.ResolutionsNew, "accepted_new", run.AcceptedNew, "changes", run.Changes, "requests", run.Requests, "rate_limited", run.RateLimited)
	// Scores read every platform's data and age by the hour, so every poll
	// that stored something rescores everything (well under a second).
	if run.Status != store.RunFailed && ctx.Err() == nil {
		if err := huntscore.Refresh(ctx, s.store, s.store.Now()); err != nil {
			log.Warn("hunt scores not updated", "err", err)
		}
		// The API's per-field updated_at: which numbers this poll changed.
		if err := s.store.TrackValues(ctx); err != nil {
			log.Warn("value times not updated", "err", err)
		}
	}
	return run, nil
}

// poll does the work of one run. listErr makes the run partial (or failed);
// statsErr (program stats or disclosed reports) is only reported.
func (s *Syncer) poll(ctx context.Context, log *slog.Logger, p platform.Platform, o Options, run *store.Run) (listErr, statsErr error) {
	s.step(run.ID, "Listing programs", 0)
	listing, recent, listErr := s.list(ctx, p, o)
	run.ListingRecent = recent
	if recent {
		log.Info("listing newest programs only", "complete_listing_every", completeListingEvery)
	}
	programs := listing.Programs
	complete := listing.Complete && listErr == nil
	if listErr != nil {
		if len(programs) == 0 {
			return listErr, nil
		}
		log.Warn("program listing incomplete; removals skipped this run", "err", listErr, "programs_listed", len(programs))
	}
	if o.Limit > 0 && len(programs) > o.Limit {
		log.Info("limiting poll", "limit", o.Limit, "listed", len(programs))
		programs, complete = programs[:o.Limit], false
	}
	if len(o.Handles) > 0 {
		programs = slices.DeleteFunc(slices.Clone(programs), func(p platform.Program) bool { return !slices.Contains(o.Handles, p.Handle) })
		complete = false
	}
	run.ListingComplete = complete
	run.ProgramsSeen = len(programs)

	if listing.ChangesErr != nil {
		log.Warn("change feed failed; scopes are downloaded without skipping unchanged ones", "err", listing.ChangesErr)
	}
	s.step(run.ID, "Saving programs", 0)
	ids, jobs, unchanged, held, err := s.syncPrograms(ctx, p.Name(), accountHash(p), run.ID, programs, listing.ChangesSince, complete, o)
	if err != nil {
		return fmt.Errorf("store programs: %w", err), nil
	}
	if held != nil {
		run.ListingComplete = false
		listErr = held
		log.Warn("removals held back", "err", held)
	}

	if il, ok := p.(platform.InviteLister); ok && ctx.Err() == nil {
		s.syncInvites(ctx, log, il, p.Name())
	}

	workers := s.concurrency
	if o.Concurrency > 0 {
		workers = o.Concurrency
	}
	s.step(run.ID, "Scope", len(jobs))
	fetched, failed, checked := s.syncScopes(ctx, log, p, run.ID, jobs, ids, workers, o.ForceRemovals)
	run.ScopesFetched, run.ScopesFailed, run.ScopesUnchanged = fetched, failed, unchanged+checked

	if len(o.Handles) > 0 {
		// RefreshPrograms fetches their stats next, however fresh; the
		// feeds below cover the whole platform, but the programs' own
		// report histories are fetched now if they haven't been (a private
		// program's shows once its invitation is accepted).
		s.step(run.ID, "Report history", 0)
		if df, ok := p.(platform.DisclosureFetcher); ok && ctx.Err() == nil {
			var err error
			run.DisclosuresNew, err = s.disclosureHistory(ctx, log, df, p.Name(), ids)
			statsErr = errors.Join(statsErr, err)
		}
		if rf, ok := p.(platform.ResolutionFetcher); ok && ctx.Err() == nil {
			var err error
			run.ResolutionsNew, err = s.resolutionHistory(ctx, log, rf, p.Name(), ids)
			statsErr = errors.Join(statsErr, err)
		}
		return listErr, statsErr
	}
	if sf, ok := p.(platform.StatsFetcher); ok && o.Mode == ModeFull && ctx.Err() == nil && !s.webSyncOn(ctx, p) {
		run.StatsFetched, run.StatsFailed, statsErr = s.syncStats(ctx, log, sf, p.Name(), run.ID, ids)
	}
	if sh, ok := p.(platform.ScopeHistorian); ok && o.Mode == ModeFull && o.Limit == 0 && ctx.Err() == nil {
		s.step(run.ID, "Scope history", 0)
		statsErr = errors.Join(statsErr, s.syncHistory(ctx, log, sh, p.Name()))
	}
	if sh, ok := p.(platform.StateHistorian); ok && o.Mode == ModeFull && o.Limit == 0 && ctx.Err() == nil {
		s.step(run.ID, "Status history", 0)
		statsErr = errors.Join(statsErr, s.syncStateHistory(ctx, log, sh, p.Name()))
	}
	if df, ok := p.(platform.DisclosureFetcher); ok && ctx.Err() == nil {
		s.step(run.ID, "Disclosed reports", 0)
		var err error
		run.DisclosuresNew, err = s.syncDisclosures(ctx, log, df, p.Name(), ids, o.Mode == ModeFull)
		statsErr = errors.Join(statsErr, err)
	}
	if rf, ok := p.(platform.ResolutionFetcher); ok && ctx.Err() == nil {
		s.step(run.ID, "Resolved reports", 0)
		var err error
		run.ResolutionsNew, err = s.syncResolutions(ctx, log, rf, p.Name(), ids, o.Mode == ModeFull)
		statsErr = errors.Join(statsErr, err)
	}
	if af, ok := p.(platform.AcceptedFetcher); ok && ctx.Err() == nil {
		s.step(run.ID, "Accepted reports", 0)
		var err error
		run.AcceptedNew, err = s.syncAccepted(ctx, log, af, p.Name(), ids, o.Mode == ModeFull)
		statsErr = errors.Join(statsErr, err)
	}
	return listErr, statsErr
}

// list reads the platform's listing: only its newest page in quick polls
// of a RecentLister when a complete listing ran within completeListingEvery,
// else all of it. recent says which.
func (s *Syncer) list(ctx context.Context, p platform.Platform, o Options) (l platform.Listing, recent bool, err error) {
	if rl, ok := p.(platform.RecentLister); ok && o.Mode == ModeQuick && o.Limit == 0 {
		last, err := s.store.LastCompleteListing(ctx, p.Name())
		if err != nil {
			return l, false, fmt.Errorf("last complete listing: %w", err)
		}
		if last != nil && s.store.Now().Sub(*last) < completeListingEvery {
			l, err = rl.ListRecent(ctx)
			l.Complete = false
			return l, true, err
		}
	}
	l, err = p.ListPrograms(ctx)
	return l, false, err
}

// IsRunInProgress reports whether err means a poll was already running.
func IsRunInProgress(err error) bool { return errors.Is(err, store.ErrRunInProgress) }

// FormatRun renders a run as a one-line summary for the CLI.
func FormatRun(r store.Run) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s poll: %s — %d programs, %d scopes fetched", r.Platform, r.Mode, r.Status, r.ProgramsSeen, r.ScopesFetched)
	if r.ScopesFailed > 0 {
		fmt.Fprintf(&b, " (%d failed)", r.ScopesFailed)
	}
	if r.ScopesUnchanged > 0 {
		fmt.Fprintf(&b, ", %d unchanged", r.ScopesUnchanged)
	}
	if r.DisclosuresNew > 0 {
		fmt.Fprintf(&b, ", %d new disclosed reports", r.DisclosuresNew)
	}
	if r.ResolutionsNew > 0 {
		fmt.Fprintf(&b, ", %d new resolved reports", r.ResolutionsNew)
	}
	if r.AcceptedNew > 0 {
		fmt.Fprintf(&b, ", %d new accepted reports", r.AcceptedNew)
	}
	if r.StatsFetched > 0 || r.StatsFailed > 0 {
		fmt.Fprintf(&b, ", %d program stats", r.StatsFetched)
		if r.StatsFailed > 0 {
			fmt.Fprintf(&b, " (%d failed)", r.StatsFailed)
		}
	}
	fmt.Fprintf(&b, ", %d changes, %d requests", r.Changes, r.Requests)
	if r.RateLimited > 0 {
		fmt.Fprintf(&b, " (%d rate-limited)", r.RateLimited)
	}
	switch {
	case r.ListingRecent:
		b.WriteString(", newest programs only")
	case !r.ListingComplete:
		b.WriteString(", listing incomplete (no removals)")
	}
	if r.Error != "" {
		fmt.Fprintf(&b, "\n  error: %s", r.Error)
	}
	return b.String()
}

// ErrPollTimeout marks a poll the scheduler stopped because it ran far longer
// than it should have. It is separate from an interruption on purpose: an
// interrupted run is retried in minutes, and a run that hung would then hang
// again in minutes, forever. This one waits for its next interval.
var ErrPollTimeout = errors.New("the poll ran past its time limit")

// pollDeadline is how long one poll may take. Measured: a full poll of
// Intigriti (235 programs, 249 requests) takes about three minutes, and
// HackerOne's listing is five times the size; these leave room for a slow
// platform without letting a stuck one hold its platform for hours.
// PollDeadline is how long a poll of this mode may take.
func PollDeadline(mode string) time.Duration {
	switch mode {
	case ModeFull:
		return 45 * time.Minute
	case ModeQuick:
		return 20 * time.Minute
	default:
		return 30 * time.Minute
	}
}
