package syncer

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/mdpsec/huntdeck/internal/huntscore"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// syncStats refreshes public stats for the programs in this run whose stats
// are older than statsMaxAge. Stats are kept for whatever was fetched before
// an error.
func (s *Syncer) syncStats(ctx context.Context, log *slog.Logger, sf platform.StatsFetcher, platformName string,
	runID int64, ids map[string]int64) (fetched, failed int, err error) {
	due, err := s.store.StatsDue(ctx, platformName, s.store.Now().Add(-statsMaxAge))
	if err != nil {
		return 0, 0, fmt.Errorf("program stats: %w", err)
	}
	var handles []string
	for _, h := range due {
		if _, ok := ids[h]; ok {
			handles = append(handles, h)
		}
	}
	if len(handles) == 0 {
		return 0, 0, nil
	}
	log.Info("fetching program stats", "programs", len(handles))
	tf, _ := sf.(platform.ThanksFetcher)
	fetched, failed, err = s.fetchStats(ctx, sf.FetchProgramStats, tf, runID, handles, ids)
	if err != nil {
		log.Warn("program stats incomplete", "err", err, "fetched", fetched, "due", len(handles))
	}
	return fetched, failed, err
}

// RefreshStats fetches public stats now for the named programs of p, or for
// all of its live programs if handles is empty, however recent their stats
// are. It writes only stats, so it doesn't take the poll lease.
func (s *Syncer) RefreshStats(ctx context.Context, p platform.Platform, handles []string) (fetched, failed int, err error) {
	sf, ok := p.(platform.StatsFetcher)
	if !ok {
		return 0, 0, fmt.Errorf("%s doesn't publish program stats", p.Name())
	}
	fetch := sf.FetchProgramStats
	if ws, ok := p.(platform.WebSyncer); ok && s.webSyncOn(ctx, p) {
		fetch = ws.WebSync // the same stats the browser sync stores
	}
	ids, err := s.store.LiveProgramIDs(ctx, p.Name())
	if err != nil {
		return 0, 0, err
	}
	if len(handles) == 0 {
		handles = slices.Sorted(maps.Keys(ids))
	}
	for _, h := range handles {
		if _, ok := ids[h]; !ok {
			return 0, 0, fmt.Errorf("%s: no stored program %q (run a poll first?)", p.Name(), h)
		}
	}
	tf, _ := p.(platform.ThanksFetcher)
	return s.fetchStats(ctx, fetch, tf, 0, handles, ids)
}

// webSyncOn reports whether p's program stats come from its browser sync:
// it has one, with a login configured, and it isn't turned off in Settings.
func (s *Syncer) webSyncOn(ctx context.Context, p platform.Platform) bool {
	ws, ok := p.(platform.WebSyncer)
	if !ok || !ws.WebSyncConfigured() {
		return false
	}
	set, _, err := s.store.WebSync(ctx, p.Name())
	return err == nil && set.Enabled
}

// WebSync runs p's browser sync: program stats for every live program from
// its logged-in website, recorded as a run in mode ModeWeb. It takes the
// platform's poll lease like a poll, so the two never write at once.
func (s *Syncer) WebSync(ctx context.Context, p platform.Platform, trigger string) (store.Run, error) {
	ws, ok := p.(platform.WebSyncer)
	if !ok || !ws.WebSyncConfigured() {
		return store.Run{}, fmt.Errorf("%s has no web login configured", p.Name())
	}
	ids, err := s.store.LiveProgramIDs(ctx, p.Name())
	if err != nil {
		return store.Run{}, err
	}
	runID, err := s.store.StartRun(ctx, p.Name(), ModeWeb, trigger, staleRunAfter)
	if err != nil {
		return store.Run{}, err
	}
	log := s.log.With("platform", p.Name(), "mode", ModeWeb, "run", runID)
	log.Info("browser sync started", "programs", len(ids))
	reqs0, limited0 := p.Stats()
	run := store.Run{ID: runID, Platform: p.Name(), Mode: ModeWeb, Trigger: trigger, ProgramsSeen: len(ids)}
	defer s.trackRun(ctx, runID, p)()
	var syncErr error
	run.StatsFetched, run.StatsFailed, syncErr = s.fetchStats(ctx, ws.WebSync, nil, runID, slices.Sorted(maps.Keys(ids)), ids)
	if lh, ok := p.(platform.LeaderboardHistorian); ok && ctx.Err() == nil && (syncErr == nil || run.StatsFetched > 0) {
		s.step(runID, "Past leaderboards", 0)
		if err := s.syncLeaderboards(ctx, log, lh, p.Name()); err != nil {
			log.Warn("past leaderboards not read", "err", err)
		}
	}
	reqs1, limited1 := p.Stats()
	run.Requests, run.RateLimited = reqs1-reqs0, limited1-limited0
	switch {
	case ctx.Err() != nil:
		run.Status, run.Error = store.RunFailed, store.RunInterrupted
	case syncErr != nil && run.StatsFetched == 0:
		run.Status, run.Error = store.RunFailed, syncErr.Error()
	case syncErr != nil || run.StatsFailed > 0:
		run.Status = store.RunPartial
		if syncErr != nil {
			run.Error = syncErr.Error()
		}
	default:
		run.Status = store.RunOK
	}
	countCtx, cancelCount := recordContext(ctx)
	if n, err := s.store.CountChanges(countCtx, runID); err == nil {
		run.Changes = n
	}
	cancelCount()
	finishCtx, cancelFinish := recordContext(ctx)
	defer cancelFinish()
	if err := s.store.FinishRun(finishCtx, run); err != nil {
		return run, fmt.Errorf("record browser sync result: %w", err)
	}
	log.Info("browser sync finished", "status", run.Status, "stats_fetched", run.StatsFetched, "stats_failed", run.StatsFailed,
		"changes", run.Changes, "requests", run.Requests, "rate_limited", run.RateLimited, "err", run.Error)
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

// fetchStats fetches stats for handles and stores each batch as it arrives,
// so an interrupted run keeps what it fetched and the next one resumes
// with the rest (see StatsDue). Rule changes are recorded against runID (0
// outside a poll).
func (s *Syncer) fetchStats(ctx context.Context, fetch func(context.Context, []string, platform.StatsSaver) error, tf platform.ThanksFetcher,
	runID int64, handles []string, ids map[string]int64) (fetched, failed int, err error) {
	covered := 0
	s.step(runID, "Program stats", len(handles))
	wctx := context.WithoutCancel(ctx) // a batch that arrived is saved even during shutdown
	err = fetch(ctx, handles, func(batch []string, stats map[string]platform.ProgramStats) error {
		defer s.advance(runID, len(batch))
		thanks, err := s.readAllThanks(ctx, tf, stats, ids)
		if err != nil {
			return err
		}
		werr := s.store.Write(wctx, func(tx *store.Tx) error {
			for h, st := range stats {
				id, ok := ids[h]
				if !ok {
					continue
				}
				prev, err := tx.KnownRules(wctx, id)
				if err != nil {
					return fmt.Errorf("%s: %w", h, err)
				}
				if t := thanks[h]; t != nil {
					st.AcceptedReports = &t.total
					if err := tx.SaveThanks(wctx, id, t.hunters, t.complete); err != nil {
						return fmt.Errorf("%s thanks: %w", h, err)
					}
				}
				switch {
				case needsCounting(st):
					base, ok, err := tx.TotalsBaseline(wctx, id, countedDays*24*time.Hour)
					if err != nil {
						return fmt.Errorf("%s saved totals: %w", h, err)
					}
					count90d(&st, base, ok)
				case needsRewardedCount(st):
					// CrowdStream's accepted reports make the better estimate
					// where the program lists them (store.ProgramListItem).
					shared, err := tx.CrowdStreamShared(wctx, id)
					if err != nil {
						return fmt.Errorf("%s crowdstream: %w", h, err)
					}
					if !shared {
						base, ok, err := tx.TotalsBaseline(wctx, id, countedDays*24*time.Hour)
						if err != nil {
							return fmt.Errorf("%s saved totals: %w", h, err)
						}
						countRewarded90d(&st, base, ok)
					}
				}
				if err := tx.RecordProgramStats(wctx, id, st); err != nil {
					return fmt.Errorf("%s: %w", h, err)
				}
				for _, c := range rulesChanges(prev, st) {
					c.PollRunID, c.ProgramID = runID, id
					if err := tx.AddChange(wctx, c); err != nil {
						return fmt.Errorf("%s: %w", h, err)
					}
				}
				if err := recordArchived(wctx, tx, id, st.Archived); err != nil {
					return fmt.Errorf("%s archived scope: %w", h, err)
				}
				if err := recordAnnouncements(wctx, tx, runID, id, st.Announcements); err != nil {
					return fmt.Errorf("%s announcements: %w", h, err)
				}
			}
			return nil
		})
		if werr != nil {
			return fmt.Errorf("store: %w", werr)
		}
		covered += len(batch)
		fetched += len(stats)
		return nil
	})
	if err != nil {
		return fetched, len(handles) - covered, fmt.Errorf("program stats: %w", err)
	}
	return fetched, 0, nil
}
