package syncer

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

const researcherStatsEvery = time.Hour

// syncResearcherStats refreshes the user's own standing (Signal, Impact,
// Reputation, Rank) on full polls, and at most once an hour on quick polls.
// Failures are logged at warning level and never mark the scope poll partial.
func (s *Syncer) syncResearcherStats(ctx context.Context, log *slog.Logger, rf platform.ResearcherStatsFetcher, platformName, mode string) {
	if mode == ModeQuick {
		cur, err := s.store.ResearcherStats(ctx, platformName)
		if err == nil && cur != nil && s.store.Now().Sub(cur.CapturedAt) < researcherStatsEvery {
			return
		}
	}
	rs, err := rf.FetchResearcherStats(ctx)
	if err != nil {
		if errors.Is(err, platform.ErrNoWebLogin) || errors.Is(err, weblogin.ErrWaiting) {
			return
		}
		log.Warn("researcher stats not read", "err", err)
		return
	}
	if err := s.store.SaveResearcherStats(ctx, platformName, rs); err != nil {
		log.Warn("researcher stats not saved", "err", err)
		return
	}
	// Never log personal numbers or username.
	log.Info("researcher stats updated")
}
