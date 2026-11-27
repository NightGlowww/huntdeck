package web

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

type researcherWindowView struct {
	Key        string
	LabelKey   string
	Checked    bool
	Signal     string
	SignalPct  string
	SignalNeg  bool
	Impact     string
	ImpactPct  string
	Reputation string
	Rank       string
}

type researcherStatsView struct {
	HasData    bool
	Username   string
	ProfileURL string
	CapturedAt time.Time
	Windows    []researcherWindowView
}

type signalCautionView struct {
	Required    float64
	RequiredStr string
	HasPastYear bool
	YoursStr    string
	Meets       bool
	CapturedAt  time.Time
}

func formatWindowView(key, labelKey string, checked bool, w platform.ResearcherWindowStats) researcherWindowView {
	out := researcherWindowView{
		Key:        key,
		LabelKey:   labelKey,
		Checked:    checked,
		Signal:     "—",
		SignalPct:  "—",
		Impact:     "—",
		ImpactPct:  "—",
		Reputation: "—",
		Rank:       "—",
	}
	if w.Signal != nil {
		out.Signal = fmt.Sprintf("%.2f", *w.Signal)
		out.SignalNeg = *w.Signal < 0
	}
	if w.SignalPercentile != nil {
		out.SignalPct = fmt.Sprintf("%d%%", *w.SignalPercentile)
	}
	if w.Impact != nil {
		out.Impact = fmt.Sprintf("%.2f", *w.Impact)
	}
	if w.ImpactPercentile != nil {
		out.ImpactPct = fmt.Sprintf("%d%%", *w.ImpactPercentile)
	}
	if w.Reputation != nil {
		out.Reputation = humanize.Thousands(strconv.Itoa(*w.Reputation))
	}
	// HackerOne displays a researcher's rank only inside the top 100.
	if w.Rank != nil && *w.Rank > 0 && *w.Rank <= 100 {
		out.Rank = fmt.Sprintf("#%d", *w.Rank)
	}
	return out
}

func (s *server) h1ResearcherStats(ctx context.Context) *researcherStatsView {
	if s.ReadOnly {
		return nil
	}
	rs, err := s.Store.ResearcherStats(ctx, "hackerone")
	if err != nil {
		s.Log.WarnContext(ctx, "read researcher stats", "platform", "hackerone", "error", err)
	}
	if rs == nil {
		hasH1 := slices.ContainsFunc(s.Platforms, func(p PlatformInfo) bool { return p.Name == "hackerone" })
		if !hasH1 {
			return nil
		}
		return &researcherStatsView{HasData: false}
	}
	v := &researcherStatsView{
		HasData:    true,
		Username:   rs.Username,
		CapturedAt: rs.CapturedAt,
		Windows: []researcherWindowView{
			formatWindowView("90d", "90 Days", true, rs.Last90Days),
			formatWindowView("1y", "Past Year", false, rs.PastYear),
			formatWindowView("all", "All time", false, rs.AllTime),
		},
	}
	if rs.Username != "" {
		v.ProfileURL = "https://hackerone.com/" + rs.Username
	}
	return v
}

func (s *server) programSignalCheck(ctx context.Context, p *store.ProgramDetail) *signalCautionView {
	if s.ReadOnly || p == nil || p.Platform != "hackerone" || p.Requirements.MinSignal == nil {
		return nil
	}
	rs, err := s.Store.ResearcherStats(ctx, "hackerone")
	if err != nil || rs == nil {
		return nil
	}
	req := *p.Requirements.MinSignal
	meets := rs.PastYear.Signal != nil && *rs.PastYear.Signal >= req
	// Keep clutter off programs with a 0/negative threshold when the user already qualifies.
	if meets && req <= 0 {
		return nil
	}
	out := &signalCautionView{
		Required:    req,
		RequiredStr: strconv.FormatFloat(req, 'f', 1, 64),
		Meets:       meets,
		CapturedAt:  rs.CapturedAt,
	}
	if rs.PastYear.Signal != nil {
		out.HasPastYear = true
		out.YoursStr = fmt.Sprintf("%.2f", *rs.PastYear.Signal)
	}
	return out
}
