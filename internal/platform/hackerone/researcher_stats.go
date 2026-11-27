package hackerone

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
)

var _ platform.ResearcherStatsFetcher = (*Client)(nil)

type gqlWindowStats struct {
	Signal           *float64 `json:"signal"`
	SignalPercentile *float64 `json:"signal_percentile"`
	Impact           *float64 `json:"impact"`
	ImpactPercentile *float64 `json:"impact_percentile"`
	Reputation       *int     `json:"reputation"`
	Rank             *int     `json:"rank"`
}

func (w gqlWindowStats) toPlatform() platform.ResearcherWindowStats {
	return platform.ResearcherWindowStats{
		Signal:           w.Signal,
		SignalPercentile: roundIntPtr(w.SignalPercentile),
		Impact:           w.Impact,
		ImpactPercentile: roundIntPtr(w.ImpactPercentile),
		Reputation:       w.Reputation,
		Rank:             w.Rank,
	}
}

func roundIntPtr(v *float64) *int {
	if v == nil {
		return nil
	}
	n := int(math.Round(*v))
	return &n
}

const researcherStatsCombinedQuery = `query ResearcherStats {
  me {
    id
    username
    signal
    signal_percentile
    impact
    impact_percentile
    reputation
    rank
    trial_report_reset_process
    last_90_days: statistics_snapshot(snapshot_type: last_90_days) {
      signal
      signal_percentile
      impact
      impact_percentile
      reputation
      rank
    }
    past_year: statistics_snapshot(snapshot_type: past_year) {
      signal
      signal_percentile
      impact
      impact_percentile
      reputation
      rank
    }
  }
}`

const researcherStatsAllTimeQuery = `query ResearcherStatsAllTime {
  me {
    id
    username
    signal
    signal_percentile
    impact
    impact_percentile
    reputation
    rank
  }
}`

const researcherStatsSnapshotQuery = `query UserProfileStatsCardSnapshot($username: String!, $snapshotType: UserStatisticsSnapshotTypeEnum!) {
  user(username: $username) {
    id
    statistics_snapshot(snapshot_type: $snapshotType) {
      signal
      signal_percentile
      impact
      impact_percentile
      reputation
      rank
    }
  }
}`

// FetchResearcherStats reads the signed-in user's Signal, Impact,
// Reputation and Rank across 90 days, past year and all time via the
// website GraphQL endpoint.
func (c *Client) FetchResearcherStats(ctx context.Context) (platform.ResearcherStats, error) {
	if !c.loggedIn() {
		return platform.ResearcherStats{}, ErrNoSession
	}
	var data struct {
		Me *struct {
			ID                 string          `json:"id"`
			Username           string          `json:"username"`
			Signal             *float64        `json:"signal"`
			SignalPercentile   *float64        `json:"signal_percentile"`
			Impact             *float64        `json:"impact"`
			ImpactPercentile   *float64        `json:"impact_percentile"`
			Reputation         *int            `json:"reputation"`
			Rank               *int            `json:"rank"`
			TrialResetOnTriage *bool           `json:"trial_report_reset_process"`
			Last90Days         *gqlWindowStats `json:"last_90_days"`
			PastYear           *gqlWindowStats `json:"past_year"`
		} `json:"me"`
	}
	err := c.webGraphQL(ctx, researcherStatsCombinedQuery, nil, &data, func() bool { return data.Me != nil })
	if err != nil {
		if strings.Contains(err.Error(), "graphql:") {
			return c.fetchResearcherStatsSeparate(ctx)
		}
		return platform.ResearcherStats{}, fmt.Errorf("hackerone: researcher stats: %w", err)
	}
	if data.Me == nil {
		return platform.ResearcherStats{}, errors.New("hackerone: researcher stats: the website session isn't signed in")
	}
	out := platform.ResearcherStats{
		Username:           strings.TrimSpace(data.Me.Username),
		TrialResetOnTriage: data.Me.TrialResetOnTriage,
		AllTime: gqlWindowStats{
			Signal:           data.Me.Signal,
			SignalPercentile: data.Me.SignalPercentile,
			Impact:           data.Me.Impact,
			ImpactPercentile: data.Me.ImpactPercentile,
			Reputation:       data.Me.Reputation,
			Rank:             data.Me.Rank,
		}.toPlatform(),
	}
	if data.Me.Last90Days != nil {
		out.Last90Days = data.Me.Last90Days.toPlatform()
	}
	if data.Me.PastYear != nil {
		out.PastYear = data.Me.PastYear.toPlatform()
	}
	return out, nil
}

func (c *Client) fetchResearcherStatsSeparate(ctx context.Context) (platform.ResearcherStats, error) {
	var allData struct {
		Me *struct {
			ID string `json:"id"`
			gqlWindowStats
			Username string `json:"username"`
		} `json:"me"`
	}
	if err := c.webGraphQL(ctx, researcherStatsAllTimeQuery, nil, &allData, func() bool { return allData.Me != nil }); err != nil {
		return platform.ResearcherStats{}, fmt.Errorf("hackerone: researcher stats: %w", err)
	}
	if allData.Me == nil {
		return platform.ResearcherStats{}, errors.New("hackerone: researcher stats: the website session isn't signed in")
	}
	out := platform.ResearcherStats{
		Username: strings.TrimSpace(allData.Me.Username),
		AllTime:  allData.Me.toPlatform(),
	}
	if out.Username == "" {
		return out, nil
	}
	for _, snap := range []struct {
		typ string
		dst *platform.ResearcherWindowStats
	}{
		{"last_90_days", &out.Last90Days},
		{"past_year", &out.PastYear},
	} {
		var sData struct {
			User *struct {
				Snapshot *gqlWindowStats `json:"statistics_snapshot"`
			} `json:"user"`
		}
		vars := map[string]any{"username": out.Username, "snapshotType": snap.typ}
		if err := c.webGraphQL(ctx, researcherStatsSnapshotQuery, vars, &sData, nil); err != nil {
			return platform.ResearcherStats{}, fmt.Errorf("hackerone: researcher stats (%s): %w", snap.typ, err)
		}
		if sData.User != nil && sData.User.Snapshot != nil {
			*snap.dst = sData.User.Snapshot.toPlatform()
		}
	}
	return out, nil
}
