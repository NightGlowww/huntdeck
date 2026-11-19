package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

type apiProgramRef struct {
	Platform string `json:"platform"`
	Handle   string `json:"handle"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Private  bool   `json:"private"`
	State    string `json:"state"`
}

func refJSON(p store.ProgramRef) apiProgramRef {
	return apiProgramRef{Platform: p.Platform, Handle: p.Handle, Name: p.Name, URL: p.URL, Private: p.Private, State: p.State}
}

type apiYourStats struct {
	Reports      int       `json:"reports"`
	ValidReports int       `json:"valid_reports"`
	BountyEarned float64   `json:"bounty_earned"`
	CapturedAt   time.Time `json:"captured_at"`
}

// apiPublicStats is a program's public stats. Null means the platform
// doesn't show that number; amounts are in currency.
type apiPublicStats struct {
	CapturedAt         time.Time              `json:"captured_at"`
	Currency           string                 `json:"currency,omitempty"`
	BountiesPaid90d    *float64               `json:"bounties_paid_90d"`
	ReportsReceived90d *int                   `json:"reports_received_90d"`
	BountiesPaidTotal  *float64               `json:"bounties_paid_total"`
	AverageBountyLow   *float64               `json:"average_bounty_low"`
	AverageBountyHigh  *float64               `json:"average_bounty_high"`
	TopBountyLow       *float64               `json:"top_bounty_low"`
	TopBountyHigh      *float64               `json:"top_bounty_high"`
	ResolvedReports    *int                   `json:"resolved_reports"`
	ResponseEfficiency *int                   `json:"response_efficiency_percent"`
	LastResolvedAt     *time.Time             `json:"last_report_resolved_at"`
	HackersThanked     *int                   `json:"hackers_thanked"`
	RewardedReports    *int                   `json:"rewarded_reports"`
	TotalReports       *int                   `json:"total_reports"`
	Reports7d          *int                   `json:"reports_7d"`
	ReportsThisMonth   *int                   `json:"reports_this_month"`
	FirstResponseHours *float64               `json:"first_response_hours"`
	ValidationHours    *float64               `json:"validation_hours"`
	TriageHours        *float64               `json:"triage_hours"`
	BountyHours        *float64               `json:"bounty_hours"`
	ResolutionHours    *float64               `json:"resolution_hours"`
	AcceptedReports    *int                   `json:"accepted_reports"`
	Hunters            *int                   `json:"hunters"`
	Hunters90d         *int                   `json:"hunters_90d"`            // researchers who earned leaderboard points in 90 days; 100 means 100 or more
	Points90d          *int                   `json:"leaderboard_points_90d"` // reputation points earned on the program in 90 days, where both leaderboards were read whole
	PointsAll          *int                   `json:"leaderboard_points_all"` // and all time
	RewardedLast90d    *float64               `json:"rewarded_90d"`
	CountedDays        *int                   `json:"reports_received_counted_days"` // reports_received_90d is worked out from this many days' counts
	LastSubmissionAt   *time.Time             `json:"last_submission_at"`
	LastAcceptedAt     *time.Time             `json:"last_accepted_at"`
	SignalRequirement  *float64               `json:"signal_requirement"`
	BySeverity         platform.SeverityStats `json:"by_severity"`
	Rewards            []platform.RewardRow   `json:"rewards,omitempty"`
}

func publicStatsJSON(s *store.ProgramStats, withRewards bool) *apiPublicStats {
	if s == nil {
		return nil
	}
	out := &apiPublicStats{
		CapturedAt: s.CapturedAt, Currency: s.Currency, BountiesPaid90d: s.BountiesPaidLast90d,
		ReportsReceived90d: s.ReportsLast90d, BountiesPaidTotal: s.BountiesPaid,
		AverageBountyLow: s.AvgBountyLow, AverageBountyHigh: s.AvgBountyHigh, TopBountyLow: s.TopBountyLow,
		TopBountyHigh: s.TopBountyHigh, ResolvedReports: s.ResolvedReports, ResponseEfficiency: s.ResponseEfficiency,
		LastResolvedAt: s.LastResolvedAt, HackersThanked: s.HackersThanked, RewardedReports: s.RewardedReports,
		TotalReports: s.TotalReports, Reports7d: s.ReportsLast7d, ReportsThisMonth: s.ReportsThisMonth, FirstResponseHours: s.FirstResponseHours,
		ValidationHours: s.ValidationHours, TriageHours: s.TriageHours, BountyHours: s.BountyHours, ResolutionHours: s.ResolutionHours,
		AcceptedReports: s.AcceptedReports, Hunters: s.Hunters, RewardedLast90d: s.RewardedLast90d, CountedDays: s.CountedDays,
		Hunters90d: s.Hunters90d, Points90d: s.Points90d, PointsAll: s.PointsAll,
		LastSubmissionAt: s.LastSubmissionAt, LastAcceptedAt: s.LastAcceptedAt, SignalRequirement: s.SignalRequirement,
		BySeverity: s.BySeverity,
	}
	if withRewards {
		out.Rewards = s.Rewards
	}
	return out
}

// apiStats returns one page of programs with public stats, filtered like
// /api/v1/programs and sorted by sort (see store.StatsSorts), plus totals
// per currency.
func (s *server) apiStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := statsFilterFrom(q)
	f.Limit = clampInt(q.Get("limit"), 100, 1, 1000)
	f.Offset = clampInt(q.Get("offset"), 0, 0, 1<<30)
	rows, total, err := s.Store.ProgramStatsList(r.Context(), f)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	// The totals cover every program the filter matches, as the Stats page's
	// do, not this page's. That is a full pass over the matching programs, so
	// it goes through the same memo the Stats page uses: the numbers only move
	// when the data does, and a page of results doesn't cost a second full
	// scan on every request.
	items, err := s.statsTotals(r.Context(), q)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	out := apiStatsListJSON{Sort: f.Sort, Total: total, Totals: statsTotalsJSON(items), Programs: []apiStatsRow{}}
	for _, r := range rows {
		out.Programs = append(out.Programs, apiStatsRow{refJSON(r.ProgramRef), r.OffersBounty, r.Wildcards, r.PaidPerReport(),
			publicStatsJSON(&r.Stats, false)})
	}
	writeJSON(w, http.StatusOK, out)
}

// apiRequirements is what a program requires of hackers; null when not known yet.
type apiRequirements struct {
	MinSignal   *float64 `json:"min_signal"`
	IDVerified  bool     `json:"id_verified"`
	Cleared     bool     `json:"cleared"`
	Application bool     `json:"application"`
	EEA         bool     `json:"eea"`        // residents of the European Economic Area only
	TwoFactor   bool     `json:"two_factor"` // the account must have 2FA on
	Terms       bool     `json:"terms"`      // the program's own terms must be accepted first
}

func reqsJSON(r store.ProgramRequirements) *apiRequirements {
	if !r.Known {
		return nil
	}
	return &apiRequirements{MinSignal: r.MinSignal, IDVerified: r.IDVerified, Cleared: r.Cleared, Application: r.Application,
		EEA: r.EEA, TwoFactor: r.TwoFactor, Terms: r.Terms}
}

func statsJSON(s *store.UserStatsSnapshot) *apiYourStats {
	if s == nil {
		return nil
	}
	return &apiYourStats{Reports: s.Reports, ValidReports: s.ValidReports, BountyEarned: s.BountyEarned, CapturedAt: s.CapturedAt}
}

func (s *server) apiPrograms(w http.ResponseWriter, r *http.Request) {
	// format=csv hands back the list as the page's export does: same filters,
	// same columns, every page of it.
	if strings.EqualFold(r.URL.Query().Get("format"), "csv") {
		s.programsCSV(w, r)
		return
	}
	s.apiProgramList(w, r, false, false)
}

// apiInvites is apiPrograms for the private programs (the Invites page),
// pending invitations first unless sort says otherwise.
func (s *server) apiInvites(w http.ResponseWriter, r *http.Request) {
	s.apiProgramList(w, r, true, false)
}

// apiProgramsV2 and apiInvitesV2 are the two in v2's shape (api_v2.go).
func (s *server) apiProgramsV2(w http.ResponseWriter, r *http.Request) {
	if strings.EqualFold(r.URL.Query().Get("format"), "csv") {
		s.programsCSV(w, r)
		return
	}
	s.apiProgramList(w, r, false, true)
}

func (s *server) apiInvitesV2(w http.ResponseWriter, r *http.Request) {
	s.apiProgramList(w, r, true, true)
}

// sinceParam reads since, an RFC 3339 time (or a duration back from now,
// as 24h or 7d); zero when not given.
func sinceParam(q url.Values) (time.Time, error) {
	if q.Get("since") == "" {
		return time.Time{}, nil
	}
	return parseSince(q.Get("since"), 0)
}

func (s *server) apiProgramList(w http.ResponseWriter, r *http.Request, invites, v2 bool) {
	q := r.URL.Query()
	f := programFilterFrom(q)
	since, err := sinceParam(q)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	f.ChangedSince = since
	if invites {
		f.Visibility = "private"
		if q.Get("sort") == "" {
			f.Sort = invitesList.DefaultSort
		}
	}
	f.Limit = clampInt(q.Get("limit"), 100, 1, 1000)
	f.Offset = clampInt(q.Get("offset"), 0, 0, 1<<30)
	progs, total, err := s.Store.Programs(r.Context(), f)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	var rows []programRow
	if isTrue(q.Get("trends")) {
		if rows, err = s.programRows(r.Context(), progs, time.Now()); err != nil {
			s.apiServerError(w, r, err)
			return
		}
	}
	times, err := s.Store.ProgramTimesOf(r.Context(), programIDs(progs))
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	out := apiProgramListJSON{Total: total, Programs: []apiProgramItem{}}
	outV2 := apiProgramListV2JSON{Total: total, Programs: []apiProgramItemV2{}}
	for i, p := range progs {
		item := apiProgramItem{refJSON(p.ProgramRef), inviteJSON(p), p.OffersBounty, p.FirstSeenAt, p.RemovedAt,
			p.InScope, p.OutOfScope, p.Wildcards, p.Domains, p.StartedAt, p.ScopeChangedAt, statsJSON(p.Stats),
			p.Credentials, reqsJSON(p.Requirements), p.Disclosed, p.Hunt, p.Ignored, nonNil(p.Tags), bountyJSON(p), p.Fresh, p.Apps, p.APIs,
			p.ResumedAt, numbersJSON(p), nil, freshnessJSON(times[p.ID])}
		if rows != nil {
			t := rows[i].Trends
			item.Trends = &apiProgramTrends{daysJSON(t.Received.points), daysJSON(t.Paid.points), daysJSON(t.Resolved.points)}
		}
		if v2 {
			outV2.Programs = append(outV2.Programs, itemV2(item, programMetaV2(p, times[p.ID])))
		} else {
			out.Programs = append(out.Programs, item)
		}
	}
	if v2 {
		writeJSON(w, http.StatusOK, outV2)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func programIDs(progs []store.ProgramListItem) []int64 {
	ids := make([]int64, len(progs))
	for i, p := range progs {
		ids[i] = p.ID
	}
	return ids
}

// freshnessJSON is a program's updated_at and last_synced_at.
func freshnessJSON(t store.ProgramTimes) apiProgramFreshness {
	return apiProgramFreshness{UpdatedAt: t.UpdatedAt, LastSyncedAt: t.LastSyncedAt}
}

// inviteJSON is a private program's invitation; nil for public ones.
func inviteJSON(p store.ProgramListItem) *apiInvite {
	if !p.Private {
		return nil
	}
	return &apiInvite{Pending: p.InvitePending, InvitedAt: p.InvitedAt, JoinedAt: p.JoinedAt}
}

// detailInviteJSON is inviteJSON for a program's full record.
func detailInviteJSON(p *store.ProgramDetail) *apiInvite {
	if !p.Private {
		return nil
	}
	return &apiInvite{Pending: p.InvitePending, InvitedAt: p.InvitedAt, JoinedAt: p.JoinedAt}
}

func (s *server) apiProgram(w http.ResponseWriter, r *http.Request) { s.apiProgramDetail(w, r, false) }

// apiProgramV2 is apiProgram in v2's shape (api_v2.go).
func (s *server) apiProgramV2(w http.ResponseWriter, r *http.Request) { s.apiProgramDetail(w, r, true) }

func (s *server) apiProgramDetail(w http.ResponseWriter, r *http.Request, v2 bool) {
	p, err := s.Store.Program(r.Context(), canonicalPlatform(r.PathValue("platform")), r.PathValue("handle"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "no such program")
		return
	}
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	var latest *store.UserStatsSnapshot
	if n := len(p.Stats); n > 0 {
		latest = &p.Stats[n-1]
	}
	// ?parts=scope,disclosed narrows the answer: a program with 5,000
	// disclosures is a megabyte an agent rarely needs all of.
	parts := strings.TrimSpace(r.URL.Query().Get("parts"))
	// An unknown part is a typo, not a request for everything: a silent 200
	// with `scope_total: 0` reads like an empty program.
	for _, p := range strings.Split(parts, ",") {
		switch strings.TrimSpace(p) {
		case "", "scope", "disclosed", "facts", "brief", "announcements", "rules", "numbers":
		case strings.TrimSpace(p):
			writeJSONError(w, http.StatusBadRequest, "unknown part: "+strings.TrimSpace(p))
			return
		}
	}
	want := func(name string) bool {
		if parts == "" {
			return true
		}
		for _, p := range strings.Split(parts, ",") {
			if strings.TrimSpace(p) == name {
				return true
			}
		}
		return false
	}

	out := apiProgramJSON{
		apiProgramRef: refJSON(p.ProgramRef), Invite: detailInviteJSON(p), OffersBounty: p.OffersBounty, Currency: p.Currency,
		FastPayments: p.FastPayments, SafeHarbor: p.SafeHarbor, StartedAt: p.StartedAt, JoinedAt: p.JoinedAt,
		FirstSeenAt: p.FirstSeenAt, LastSeenAt: p.LastSeenAt, RemovedAt: p.RemovedAt, ScopeSyncedAt: p.ScopeSyncedAt,
		ScopeCheckedAt: p.ScopeCheckedAt, ScopeUnavailableAt: p.ScopeUnavailableAt,
		Stats: statsJSON(latest), PublicStats: publicStatsJSON(p.PublicStats, true), Scope: []apiScopeEntry{},
		Hunt: p.Hunt, Ignored: p.Ignored, Tags: nonNil(p.Tags),
		Requirements: reqsJSON(p.Requirements), TestCredentials: nonNil(p.CredentialAssets), Testing: testingJSON(p.Testing),
	}
	if want("rules") {
		for _, r := range beforeYouTest(p) {
			out.Rules = append(out.Rules, apiTestRule(r))
		}
	}
	if want("facts") {
		out.Facts = factsJSON(p)
	}
	if want("brief") {
		out.Brief = briefJSON(p)
	}
	if want("announcements") {
		out.Announcements = announcementsJSON(p.PublicStats)
	}
	items, _, err := s.Store.Programs(r.Context(), store.ProgramFilter{ID: p.ID})
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	times, err := s.Store.ProgramTimesOf(r.Context(), []int64{p.ID})
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	out.apiProgramFreshness = freshnessJSON(times[p.ID])
	var meta *apiProgramMetaV2
	if len(items) > 0 {
		meta = ptrTo(programMetaV2(items[0], times[p.ID]))
		rows, err := s.programRows(r.Context(), items, time.Now())
		if err != nil {
			s.apiServerError(w, r, err)
			return
		}
		// The derived numbers are a projection of the same data and cost a query
		// each: a caller asking for `facts` alone does not need them.
		if want("numbers") {
			t := rows[0].Trends
			out.Bounty, out.Numbers = bountyJSON(items[0]), ptrTo(numbersJSON(items[0]))
			out.Trends = &apiProgramTrends{daysJSON(t.Received.points), daysJSON(t.Paid.points), daysJSON(t.Resolved.points)}
		}
	}
	if want("scope") {
		// Entries are the unit: a program can carry thousands, each with its own
		// targets, so the answer pages them.
		scopeLimit := clampInt(r.URL.Query().Get("scope_limit"), 500, 1, 5000)
		scopeOffset := clampInt(r.URL.Query().Get("scope_offset"), 0, 0, 1<<30)
		out.ScopeTotal = len(p.Entries)
		entries := p.Entries
		if scopeOffset < len(entries) {
			entries = entries[scopeOffset:]
		} else {
			entries = nil
		}
		if len(entries) > scopeLimit {
			entries = entries[:scopeLimit]
		}
		for _, e := range entries {
			ej := apiScopeEntry{e.InScope, e.AssetType, e.PlatformAssetType, e.Identifier, e.BountyEligible, e.Credentials, e.MaxSeverity,
				e.Instruction, e.FirstSeenAt, e.AddedAt, e.RemovedAt, []apiScopeTarget{}}
			for _, t := range e.Targets {
				ej.Targets = append(ej.Targets, apiScopeTarget{t.Kind, t.Value})
			}
			out.Scope = append(out.Scope, ej)
		}
	}
	reports, sum, err := s.Store.ProgramDisclosures(r.Context(), p.ID, apiDisclosures)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	if want("disclosed") {
		out.Disclosed = disclosuresJSON(reports, sum)
	}
	res, err := s.Store.ProgramResolutions(r.Context(), p.ID, time.Now(), 24)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	out.Resolved = resolutionsJSON(res)
	acc, err := s.Store.ProgramAccepted(r.Context(), p.ID, time.Now(), 24)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	out.Accepted = acceptedReportsJSON(acc)
	if v2 {
		writeJSON(w, http.StatusOK, programV2(out, meta))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// apiBounty is the bounty range a program pays and where it comes from:
// "table", "listing" or "stats" (only the most it has paid).
type apiBounty struct {
	Min      *float64 `json:"min"`
	Max      *float64 `json:"max"`
	Currency string   `json:"currency"`
	From     string   `json:"from"`
}

func bountyJSON(p store.ProgramListItem) *apiBounty {
	if p.BountyMax == nil {
		return nil
	}
	return &apiBounty{p.BountyMin, p.BountyMax, p.Currency, p.BountyFrom}
}

// statsTotals is /api/v1/stats's totals for a filter: every program it matches,
// loaded once per data version (store.Memo). The warmer calls it too, so the
// first request after a restart does not pay for a full pass.
func (s *server) statsTotals(ctx context.Context, q url.Values) ([]store.ProgramListItem, error) {
	f := statsFilterFrom(q)
	all := f
	all.Limit, all.Offset = 1<<20, 0
	items, err := store.Memo(ctx, s.Store, "stats-totals?"+facetKey(q, programFacetParams...),
		func(ctx context.Context) ([]store.ProgramListItem, error) {
			items, _, err := s.Store.Programs(ctx, all)
			return items, err
		})
	if err != nil {
		return nil, err
	}
	return items, nil
}
