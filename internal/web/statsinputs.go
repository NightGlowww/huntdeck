package web

import (
	"context"
	"net/url"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// What the Stats pages are built from takes a second or more to read, so
// it's memoised (store.Memo) and Warm works it out again after each poll,
// before anyone asks.

// statsInputs are the Stats pages' numbers for every platform: each
// platform's overview, its open programs as the Programs list has them,
// and their daily numbers by column (statsDays), then platform.
type statsInputs struct {
	Start     time.Time // the first month charted
	Overviews []store.PlatformOverview
	Items     []store.ProgramListItem
	Days      map[string]map[string][]store.ProgramDay
}

// statsStart is the first month the Stats pages chart, now being now.
func statsStart(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -(overviewMonths - 1), 0)
}

// statsInputs reads statsInputs for the programs of visibility ("public",
// "private", or "" for both; see statsVisibility), memoised.
func (s *server) statsInputs(ctx context.Context, visibility string) (statsInputs, error) {
	now := time.Now().UTC()
	start := statsStart(now)
	return store.Memo(ctx, s.Store, "stats:"+visibility+":"+start.Format(time.DateOnly), func(ctx context.Context) (statsInputs, error) {
		in := statsInputs{Start: start, Days: map[string]map[string][]store.ProgramDay{}}
		var err error
		if in.Overviews, err = s.Store.PlatformOverviews(ctx, now, start, overviewMonths, "", visibility); err != nil {
			return in, err
		}
		if in.Items, _, err = s.Store.Programs(ctx, store.ProgramFilter{State: "open", Ignored: "include", Visibility: visibility, Limit: 1 << 20}); err != nil {
			return in, err
		}
		for _, col := range statsDays {
			if in.Days[col], err = s.Store.PlatformDays(ctx, col, now.AddDate(-1, 0, 0), "", visibility); err != nil {
				return in, err
			}
		}
		return in, nil
	})
}

// programSeries reads every live program's months (store.ProgramSeries)
// of visibility, memoised: a platform's stats page lists its programs with
// them.
func (s *server) programSeries(ctx context.Context, visibility string) (map[int64]store.PlatformOverview, error) {
	now := time.Now().UTC()
	start := statsStart(now)
	return store.Memo(ctx, s.Store, "stats-series:"+visibility+":"+start.Format(time.DateOnly), func(ctx context.Context) (map[int64]store.PlatformOverview, error) {
		return s.Store.ProgramSeries(ctx, "", visibility, now, start, overviewMonths)
	})
}

// warmEvery is how often Warm looks for changes.
const warmEvery = 30 * time.Second

// Warm keeps the Stats pages' numbers worked out, so the first view after
// a poll needn't wait for them: once the database has been still for
// warmEvery (a poll writes all the way through), it reads them, which
// works them out again if anything changed. It returns when ctx is done.
func Warm(ctx context.Context, d Deps) {
	s := &server{Deps: d}
	t := time.NewTicker(warmEvery)
	defer t.Stop()
	last := int64(-1)
	// Right away: a restart empties the caches, and the first visitor should
	// not pay for the whole Stats page. The stillness check still applies to
	// later rounds, where a poll may be writing.
	first := true
	for {
		v, err := s.Store.DataVersion(ctx)
		switch {
		case err != nil:
		case first, v == last:
			first = false
			last = v
			t0 := time.Now()
			s.warmStats(ctx)
			s.Log.Debug("warm done", "took", time.Since(t0).Round(time.Millisecond))
		default:
			last = v // data moved: wait for it to settle
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// warmStats reads what the Stats pages and /api/v1/stats work out, so both
// are ready before anyone asks.
func (s *server) warmStats(ctx context.Context) {
	// In the order a visitor meets them: the two landing pages first (the board
	// and the Programs list), then the rest. Warming the Stats pages first left
	// the first visitor of a restart waiting for them 200 ms.
	if _, err := s.summaryMemo(ctx); err != nil {
		s.warnWarm(ctx, err)
	}
	if _, err := s.topHuntsMemo(ctx); err != nil {
		s.warnWarm(ctx, err)
	}
	if _, err := s.boardInvitesMemo(ctx); err != nil {
		s.warnWarm(ctx, err)
	}
	if _, err := s.changeCounts(ctx, nil); err != nil {
		s.warnWarm(ctx, err)
	}
	s.warmLists(ctx)
	if _, err := s.statsInputs(ctx, ""); err != nil { // the default view, All
		s.warnWarm(ctx, err)
	}
	if _, err := s.programSeries(ctx, ""); err != nil {
		s.warnWarm(ctx, err)
	}
	if _, err := s.statsTotals(ctx, url.Values{}); err != nil {
		s.warnWarm(ctx, err)
	}
	// The Targets page is warmed beside the rest, not before it: its counts are
	// the heaviest part and nobody is waiting for them at the door.
	go func() {
		s.warmTargets(ctx)
		s.warmTargetFacets(ctx)
	}()
}

func (s *server) warnWarm(ctx context.Context, err error) {
	if err != nil && ctx.Err() == nil {
		s.Log.Warn("cannot work out the stats pages' numbers ahead", "err", err)
	}
}
