package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// ProgramFilter selects programs for the programs list.
type ProgramFilter struct {
	// ID, if set, picks that one program, whatever its state, whether it's
	// ignored, and every other filter (a program's own page).
	ID int64
	// Platforms, if any, keeps to programs on one of them.
	Platforms []string
	// Watch, if any, keeps to these programs: a browser's watchlist.
	Watch []ProgramKey
	// Noted, if any, keeps to these programs too: those a browser has
	// notes on.
	Noted      []ProgramKey
	Visibility string // "", "private" or "public"
	// Invite is "" (any), "pending" (invitations not accepted yet) or
	// "joined" (private programs whose invitation was accepted, or never
	// listed as pending).
	Invite     string
	State      string // "" or "open" (default), "all", "paused" (paused or closed), "removed"
	BountyOnly bool
	NoBounty   bool // keeps programs that don't pay bounties (VDPs)
	// CredentialsOnly keeps programs that hand out test credentials.
	CredentialsOnly bool
	Query           string // matches name or handle
	// Exclude leaves out programs whose name or handle contains any of
	// these, ignoring case.
	Exclude []string
	// ResumedSince, if set, keeps open programs that went from paused or
	// closed to open since then.
	ResumedSince time.Time
	// Ignored is "" (leave ignored programs out, the default), "include"
	// or "only".
	Ignored string
	Tag     string // keeps programs with this tag
	// MinBounty keeps programs whose top bounty (TopBountyExpr) is at least
	// this, in the program's own currency.
	MinBounty float64
	// NewScopeSince keeps programs with in-scope entries added since then
	// (dates known exactly, not "there by then").
	NewScopeSince time.Time
	// LaunchedSince keeps programs launched since then.
	LaunchedSince time.Time
	// ChangedSince keeps programs whose ProgramTimes.UpdatedAt is after it.
	ChangedSince time.Time
	Wildcards    bool // has in-scope wildcards
	Apps         bool // has in-scope Android or iOS apps
	APIs         bool // has in-scope APIs
	// NoRequirements keeps programs known to need nothing beyond an account.
	NoRequirements bool
	// Sort is "name" (default), "new", "invited" (pending invitations
	// first, then by when invited or joined, newest first), "scope", "wildcards", "domains",
	// "launched", "updated", "paid90", "paidall" (most bounties paid all
	// time), "avgbounty" (highest average bounty, a range's midpoint), "disclosed", "resumed", "huntscore",
	// "bounty" (top bounty), "fresh" (most scope added in the last 30
	// days), "quiet" (HuntScore's competition part, least crowded first) or
	// "response" (quickest first response or validation), "resolved"
	// (most resolved in 90 days) or "received" (most reports received in 90
	// days); ProgramStatsList has its own keys.
	Sort string
	// Reverse turns the sort's usual direction around; programs missing the
	// value sorted on stay last either way.
	Reverse bool
	Limit   int
	Offset  int
}

// ProgramListItem is one row of the programs list.
type ProgramListItem struct {
	ProgramRef
	OffersBounty   bool
	StartedAt      *time.Time // launch date, where the platform shares it
	FirstSeenAt    time.Time
	RemovedAt      *time.Time
	ScopeChangedAt *time.Time // latest scope change recorded, or dated by scope history
	InScope        int
	OutOfScope     int
	Wildcards      int
	Domains        int
	Stats          *UserStatsSnapshot
	// Paid90d, PaidAll (bounties paid all time) and Reports90d come from the
	// latest public stats, if any; amounts are in StatsCurrency. Where the
	// platform publishes no total but a count of rewarded reports and an
	// average payout (Bugcrowd), PaidAll is their product and
	// PaidAllEstimated is set; Rewarded and AvgPayout are those two.
	// Likewise, where it publishes no Paid90d but lists accepted reports for
	// the whole 90 days, Paid90d is the payable ones (PaidAccepted90d: all but
	// priority 5) times the average payout, and Paid90dEstimated is set.
	Paid90d          *float64
	Paid90dEstimated bool
	PaidAccepted90d  *int
	PaidAll          *float64
	PaidAllEstimated bool
	Rewarded         *int
	AvgPayout        *float64
	// AvgBountyLow is the low end of the platform's average bounty, which
	// HackerOne gives as a range (AvgPayout is the high end); the two match
	// where it gives one figure.
	AvgBountyLow *float64
	Reports90d   *int
	// CountedDays is set where Reports90d and Paid90d were worked out from
	// the running totals HuntDeck saved (YesWeHack, Intigriti): how many
	// days of them there were. Under 90, the two are estimates.
	CountedDays *int
	// TotalReports is the platform's all-time report count, where it
	// publishes one (YesWeHack, Intigriti): the total CountedDays counts from.
	TotalReports *int
	// Hunters is the platform's count of researchers on the program
	// (HackerOne: hackers thanked; see platform.ProgramStats.Hunters).
	Hunters *int
	// Points90d, PointsAll and Hunters90d are from the program's
	// leaderboards (see platform.ProgramStats).
	Points90d, PointsAll, Hunters90d *int
	StatsCurrency                    string
	Credentials                      bool // hands out test credentials for some scope
	Requirements                     ProgramRequirements
	Disclosed                        int        // publicly disclosed reports
	ResumedAt                        *time.Time // last went from paused or closed to open, if known
	// InvitePending is true while the user's invitation to this private
	// program waits to be accepted; InvitedAt is when it was sent and
	// JoinedAt when it was accepted, where known.
	InvitePending       bool
	InvitedAt, JoinedAt *time.Time
	// ListedAt is when HuntDeck recorded the program as new and private:
	// nil for one of the platform's first import or a newly signed-in
	// account's, which were the user's already.
	ListedAt *time.Time
	Hunt     *HuntDetail
	Ignored  bool
	Tags     []string

	// The bounty range it pays (BountyMin nil when only a top is known) and
	// where it comes from: PayFromTable, PayFromListing or PayFromStats.
	// Amounts are in Currency.
	BountyMin, BountyMax *float64
	BountyFrom           string
	Currency             string
	Rewards              []platform.RewardRow // its bounty table, if known
	// Report volume and response times from the latest public stats, where
	// the platform has them; Accepted90d is Bugcrowd's CrowdStream count.
	Reports7d, ReportsMonth, Accepted90d *int
	FirstResponseHours, ValidationHours  *float64
	// In-scope entries: added in the last 30 days (FreshWindow), apps and
	// APIs.
	Fresh, Apps, APIs int
	// Resolved90d counts reports resolved in the last 90 days (HackerOne
	// Hacktivity, Bugcrowd CrowdStream). Where the platform lists none but
	// publishes totals (YesWeHack, Intigriti) it is an estimate of valid
	// reports in 90 days (Resolved90dEstimated; see validEstimate); nil
	// otherwise.
	Resolved90d          *int
	Resolved90dEstimated bool
	// AcceptedAll is the platform's all-time count of the program's
	// accepted reports, where it publishes one (YesWeHack, Intigriti).
	AcceptedAll *int
}

// FreshWindow is how far back ProgramListItem.Fresh and the "fresh" sort
// look for added scope.
const FreshWindow = 30 * 24 * time.Hour

// likeEscape escapes LIKE wildcards in user input (used with ESCAPE '\').
func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(strings.ToLower(s)) + "%"
}

// ProgramKey names a program as its page's address does.
type ProgramKey struct{ Platform, Handle string }

// watchCond keeps the programs (as p) keys name.
func watchCond(keys []ProgramKey) (string, []any) {
	parts := make([]string, len(keys))
	args := make([]any, 0, 2*len(keys))
	for i, k := range keys {
		parts[i] = "(p.platform = ? AND p.handle = ?)"
		args = append(args, k.Platform, k.Handle)
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

// Amounts in different currencies can't be compared, so the money sorts
// order within currencies: the lead one's (the currency most programs'
// figures are in) first, then each other's, A to Z, each biggest first;
// programs without the amount last. Turned around, each group's amounts
// are, the groups' order stays.
const (
	statsCurrency     = "COALESCE(ps.currency, '')"
	leadStatsCurrency = `(SELECT COALESCE(ps2.currency, '') FROM programs p2
		JOIN program_public_stats ps2 ON ps2.program_id = p2.id AND ps2.captured_at = p2.stats_at
		WHERE p2.removed_at IS NULL GROUP BY 1 ORDER BY COUNT(*) DESC, 1 LIMIT 1)`
	leadProgramCurrency = `(SELECT COALESCE(p2.currency, '') FROM programs p2
		WHERE p2.removed_at IS NULL GROUP BY 1 ORDER BY COUNT(*) DESC, 1 LIMIT 1)`
)

// moneyOrder is the order for amount (biggest first) in currency, whose
// most common value lead (a subquery) names; see above.
func moneyOrder(amount, currency, lead string) string {
	return amount + " IS NULL, " + currency + " <> " + lead + keepOrder + ", " + currency + keepOrder + ", " + amount + " DESC, lower(p.name)"
}

// reverseTies turns order around if reverse, but for its tie-break by
// name, which stays A to Z: a run of programs with the same number lists
// alphabetically either way.
func reverseTies(order string, reverse bool) string {
	if !reverse {
		return order
	}
	if base, ok := strings.CutSuffix(order, ", lower(p.name)"); ok {
		return reverseOrder(base) + ", lower(p.name)"
	}
	return reverseOrder(order)
}

func (f ProgramFilter) where() (string, []any) {
	if f.ID != 0 {
		return " WHERE p.id = ?", []any{f.ID}
	}
	var conds []string
	var args []any
	switch f.State {
	case "all":
		conds = append(conds, "p.removed_at IS NULL")
	case "paused":
		conds = append(conds, "p.removed_at IS NULL", "p.state <> 'open'")
	case "removed":
		conds = append(conds, "p.removed_at IS NOT NULL")
	default:
		conds = append(conds, "p.removed_at IS NULL", "p.state = 'open'")
	}
	if len(f.Platforms) > 0 {
		conds = append(conds, "p.platform IN ("+placeholders(len(f.Platforms))+")")
		for _, p := range f.Platforms {
			args = append(args, p)
		}
	}
	for _, keys := range [][]ProgramKey{f.Watch, f.Noted} {
		if len(keys) > 0 {
			cond, wargs := watchCond(keys)
			conds, args = append(conds, cond), append(args, wargs...)
		}
	}
	if !f.ChangedSince.IsZero() {
		at := ts(f.ChangedSince)
		conds, args = append(conds, changedSinceCond), append(args, at, at, at, at)
	}
	switch f.Visibility {
	case "private":
		conds = append(conds, "p.private = 1")
	case "public":
		conds = append(conds, "p.private = 0")
	}
	switch f.Invite {
	case "pending":
		conds = append(conds, invitePendingCond)
	case "joined":
		conds = append(conds, inviteJoinedCond)
	}
	if f.BountyOnly {
		conds = append(conds, "p.offers_bounty = 1")
	}
	if f.NoBounty {
		conds = append(conds, "p.offers_bounty = 0")
	}
	if f.CredentialsOnly {
		conds = append(conds, testAccountsCond)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		conds = append(conds, `(lower(p.name) LIKE ? ESCAPE '\' OR lower(p.handle) LIKE ? ESCAPE '\')`)
		args = append(args, likeEscape(q), likeEscape(q))
	}
	for _, x := range f.Exclude {
		if x = strings.TrimSpace(x); x != "" {
			conds = append(conds, `NOT (lower(p.name) LIKE ? ESCAPE '\' OR lower(p.handle) LIKE ? ESCAPE '\')`)
			args = append(args, likeEscape(x), likeEscape(x))
		}
	}
	if !f.ResumedSince.IsZero() {
		conds = append(conds, "p.state = 'open'", resumedAt+" >= ?")
		args = append(args, ts(f.ResumedSince))
	}
	switch f.Ignored {
	case "include":
	case "only":
		conds = append(conds, "p.ignored = 1")
	default:
		conds = append(conds, "p.ignored = 0")
	}
	if f.Tag != "" {
		conds = append(conds, tagCond)
		args = append(args, f.Tag)
	}
	if f.MinBounty > 0 {
		conds = append(conds, TopBountyExpr+" >= ?")
		args = append(args, f.MinBounty)
	}
	if !f.NewScopeSince.IsZero() {
		conds = append(conds, newScopeCond)
		args = append(args, ts(f.NewScopeSince))
	}
	if !f.LaunchedSince.IsZero() {
		conds = append(conds, "p.started_at >= ?")
		args = append(args, ts(f.LaunchedSince))
	}
	if f.Wildcards {
		conds = append(conds, "p.n_wildcards > 0")
	}
	if f.Apps {
		conds = append(conds, appCount+" > 0")
	}
	if f.APIs {
		conds = append(conds, apiCount+" > 0")
	}
	if f.NoRequirements {
		conds = append(conds, noRequirementsCond)
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// TopBountyExpr is a program's top bounty, as targetBounty finds it: the
// highest in its bounty table, else the top of the range its listing gives,
// else the most its stats show it paid (0 if none); in its currency.
const TopBountyExpr = `COALESCE(
	NULLIF((SELECT MAX(MAX(COALESCE(json_extract(r.value, '$.critical.max'), 0), COALESCE(json_extract(r.value, '$.high.max'), 0),
		COALESCE(json_extract(r.value, '$.medium.max'), 0), COALESCE(json_extract(r.value, '$.low.max'), 0)))
		FROM json_each(COALESCE(p.rewards, '[]')) r), 0),
	NULLIF(p.bounty_max, 0),
	(SELECT top_bounty_high FROM program_public_stats x WHERE x.program_id = p.id AND x.captured_at = p.stats_at), 0)`

// apiKinds are the asset types counted as APIs.
const apiKinds = `('api')`

// appCount and apiCount count a program's live in-scope apps and APIs.
const (
	appCount = `(SELECT COUNT(*) FROM scope_entries e WHERE e.program_id = p.id AND e.in_scope = 1
	AND e.removed_at IS NULL AND e.asset_type IN ` + appKinds + `)`
	apiCount = `(SELECT COUNT(*) FROM scope_entries e WHERE e.program_id = p.id AND e.in_scope = 1
	AND e.removed_at IS NULL AND e.asset_type IN ` + apiKinds + `)`
)

// competitionPart is the competition part of a program's HuntScore, NULL
// unless known.
const competitionPart = `(SELECT json_extract(j.value, '$.score') FROM json_each(p.hunt_detail, '$.parts') j
	WHERE json_extract(j.value, '$.key') = 'competition' AND json_extract(j.value, '$.known'))`

// paidAllExpr is bounties paid all time: the platform's total, else rewarded
// reports times the average payout where it publishes those (an estimate).
const paidAllExpr = `COALESCE(ps.bounties_paid, ps.rewarded_reports * ps.avg_bounty_high)`

// payablePriority leaves out accepted reports of priority 5 (informational),
// which are seldom paid; reports without a priority are kept.
const payablePriority = `COALESCE(a.priority, 0) < 5`

// paidAcceptedExpr counts a program's payable accepted reports in the last
// 90 days, where its platform lists them for the whole 90 days (nil
// otherwise). It needs the start of the 90 days twice.
const paidAcceptedExpr = `CASE WHEN p.accepted_since IS NOT NULL AND p.accepted_since <= ? THEN
	(SELECT COUNT(*) FROM accepted_reports a WHERE a.program_id = p.id AND a.accepted_at >= ? AND ` + payablePriority + `) END`

// newScopeCond needs the start of the window as its argument.
const newScopeCond = `EXISTS (SELECT 1 FROM scope_entries e WHERE e.program_id = p.id AND e.in_scope = 1
	AND e.removed_at IS NULL AND e.added_bound = 0 AND e.added_at >= ?)`

// noRequirementsCond is true for programs known to need nothing beyond an
// account: no Signal minimum, ID check, clearance, application or residency.
const noRequirementsCond = `(p.req_known = 1 AND COALESCE(p.req_min_signal, 0) = 0 AND p.req_id_verified = 0
	AND p.req_cleared = 0 AND p.req_application = 0 AND p.req_eea = 0)`

// plainSorts are the Programs sorts on programs' own columns.
var plainSorts = map[string]bool{"": true, "new": true, "invited": true, "launched": true, "updated": true, "disclosed": true, "huntscore": true}

// The Invite filter's conditions.
const (
	invitePendingCond = "p.invite_pending = 1"
	inviteJoinedCond  = "(p.private = 1 AND p.invite_pending = 0)"
)

// invitedOrder is the "invited" sort: pending invitations first, then the
// latest invited or joined (or first seen, when neither is known).
const invitedOrder = "p.invite_pending DESC, COALESCE(p.invited_at, p.joined_at, p.first_seen_at) DESC, lower(p.name)"

// Programs returns one page of programs and the total matching count.
func (s *Store) Programs(ctx context.Context, f ProgramFilter) ([]ProgramListItem, int, error) {
	where, args := f.where()
	var total int
	if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM programs p`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	order := "lower(p.name), p.id"
	switch f.Sort {
	case "new":
		order = "p.first_seen_at DESC, p.id DESC"
	case "scope":
		order = "in_scope DESC, lower(p.name)"
	case "wildcards":
		order = "p.n_wildcards DESC, in_scope DESC, lower(p.name)"
	case "domains":
		order = "p.n_domains DESC, in_scope DESC, lower(p.name)"
	case "invited":
		order = invitedOrder
	case "launched":
		order = "p.started_at IS NULL, p.started_at DESC, lower(p.name)"
	case "updated":
		order = "p.scope_changed_at IS NULL, p.scope_changed_at DESC, lower(p.name)"
	case "paid90":
		order = moneyOrder("paid90_any", statsCurrency, leadStatsCurrency)
	case "avgbounty":
		order = moneyOrder("avg_bounty", statsCurrency, leadStatsCurrency)
	case "paidall":
		order = moneyOrder("paid_all", statsCurrency, leadStatsCurrency)
	case "disclosed":
		order = "p.n_disclosed DESC, lower(p.name)"
	case "resumed":
		order = "resumed_at IS NULL, resumed_at DESC, lower(p.name)"
	case "huntscore":
		order = "p.hunt_score IS NULL, p.hunt_score DESC, lower(p.name)"
	case "bounty":
		order = moneyOrder("top_bounty", "COALESCE(p.currency, '')", leadProgramCurrency)
	case "fresh":
		order = "fresh DESC, p.hunt_score IS NULL, p.hunt_score DESC, lower(p.name)"
	case "quiet":
		order = "quiet IS NULL, quiet DESC, lower(p.name)"
	case "response":
		order = "response IS NULL, response, lower(p.name)"
	case "resolved":
		order = "resolved90 IS NULL, resolved90 DESC, lower(p.name)"
	case "received":
		order = "ps.reports_90d IS NULL, ps.reports_90d DESC, lower(p.name)"
	}
	order = reverseTies(order, f.Reverse)
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	now := s.Now()
	q := selectProgramList + where + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?` //nolint:gosec // constant fragments; values are bound parameters
	if plainSorts[f.Sort] {
		// The page's programs first, then their columns: most are worked
		// out per program, so for a page of the whole list. Ties go by id
		// so both agree.
		order += ", p.id"
		q = selectProgramList + ` WHERE p.id IN (SELECT p.id FROM programs p` + where + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?)
		ORDER BY ` + order //nolint:gosec // constant fragments; values are bound parameters
	}
	since90 := ts(now.Add(-90 * 24 * time.Hour))
	args = append([]any{since90, ts(now.Add(-FreshWindow)), since90, since90, since90, since90, since90, since90}, args...)
	rows, err := s.db.R.QueryContext(ctx, q, append(args, limit, max(0, f.Offset))...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	return scanProgramList(rows, total)
}

// testAccountsCond is true for programs whose latest profile lists scope
// entries with test credentials, or a pool of them for the whole program.
const testAccountsCond = `(COALESCE(json_array_length(p.profile, '$.credential_scopes'), 0) > 0
	OR COALESCE(json_type(p.profile, '$.credential_pool'), '') = 'object')`

// selectProgramList reads the cached scope counts kept on each program
// (see RefreshScopeCounts) plus the latest stats snapshots.
// resumedAt is when a program last went from paused or closed to open,
// leaving out reopenings while it was flip-flopping (see FlipFlops).
const resumedAt = `(SELECT MAX(c.occurred_at) FROM changes c
	WHERE c.program_id = p.id AND c.kind = 'state_changed' AND c.field = 'state' AND c.new_value = 'open'
		AND ` + flipsAround + ` < 3)`

// validEstimate estimates a program's valid reports in 90 days where the
// platform lists no report dates but publishes all-time totals of reports
// and of accepted ones (YesWeHack, Intigriti): reports received in 90 days
// × the share of all its reports accepted.
const validEstimate = `CAST(ROUND(MIN(ps.accepted_reports, 1.0 * ps.reports_90d * ps.accepted_reports / ps.total_reports)) AS INTEGER)`

const selectProgramList = `
		WITH st AS (
			SELECT program_id, captured_at, reports, valid_reports, bounty_earned,
				ROW_NUMBER() OVER (PARTITION BY program_id ORDER BY captured_at DESC) AS rn
			FROM program_user_stats
		)
		SELECT p.id, p.platform, p.handle, p.name, p.url, p.private, p.state, p.offers_bounty, p.started_at, p.first_seen_at, p.removed_at,
			p.scope_changed_at, p.n_in_scope AS in_scope, p.n_out_scope, p.n_wildcards, p.n_domains,
			st.captured_at, st.reports, st.valid_reports, st.bounty_earned,
			ps.bounties_paid_90d, ps.bounties_paid, ps.reports_90d, COALESCE(ps.currency, ''), ` + testAccountsCond + `, p.n_disclosed, ` + resumedAt + ` AS resumed_at,
			p.hunt_detail, p.ignored, ` + tagsColumn + `, ` + requirementColumns + `,
			p.currency, p.rewards, p.bounty_min, p.bounty_max, ps.top_bounty_high, ps.reports_7d, ps.reports_month,
			CASE WHEN p.accepted_since IS NOT NULL THEN (SELECT COUNT(*) FROM accepted_reports a WHERE a.program_id = p.id AND a.accepted_at >= ?) END,
			ps.first_response_hours, ps.validation_hours,
			(SELECT COUNT(*) FROM scope_entries e WHERE e.program_id = p.id AND e.in_scope = 1 AND e.removed_at IS NULL
				AND e.added_bound = 0 AND e.added_at >= ?) AS fresh,
			CASE WHEN p.resolutions_synced_at IS NOT NULL OR p.accepted_since IS NOT NULL THEN
				(SELECT COUNT(*) FROM resolved_reports r WHERE r.program_id = p.id AND r.resolved_at >= ?)
				+ (SELECT COUNT(*) FROM accepted_reports a WHERE a.program_id = p.id AND a.resolved_at >= ?)
				ELSE ` + validEstimate + ` END AS resolved90,
			p.resolutions_synced_at IS NULL AND p.accepted_since IS NULL AND ` + validEstimate + ` IS NOT NULL, ps.accepted_reports,
			` + appCount + `, ` + apiCount + `,
			NULLIF(` + TopBountyExpr + `, 0) AS top_bounty, ` + competitionPart + ` AS quiet,
			COALESCE(ps.first_response_hours, ps.validation_hours) AS response,
			ps.rewarded_reports, ps.avg_bounty_high, ` + paidAllExpr + ` AS paid_all,
			` + paidAcceptedExpr + ` AS paid_accepted,
			COALESCE(ps.bounties_paid_90d, ` + paidAcceptedExpr + ` * ps.avg_bounty_high) AS paid90_any,
			ps.avg_bounty_low, (COALESCE(ps.avg_bounty_low, ps.avg_bounty_high) + ps.avg_bounty_high) / 2 AS avg_bounty,
			ps.counted_days, ps.total_reports, COALESCE(ps.hunters, ps.hackers_thanked),
			p.invite_pending, p.invited_at, p.joined_at, ps.points_90d, ps.points_all, ps.hunters_90d, ` + listedPrivate + `
		FROM programs p
		LEFT JOIN st ON st.program_id = p.id AND st.rn = 1
		LEFT JOIN program_public_stats ps ON ps.program_id = p.id AND ps.captured_at = p.stats_at`

func scanProgramList(rows *sql.Rows, total int) ([]ProgramListItem, int, error) {
	var out []ProgramListItem
	for rows.Next() {
		var it ProgramListItem
		var firstSeen string
		var started, removed, changed, captured, resumed, hunt sql.NullString
		var tags string
		var reports, valid sql.NullInt64
		var earned, paid90, paidAll sql.NullFloat64
		var reports90, r7, rm, acc, res90 sql.NullInt64
		var rewards sql.NullString
		var bmin, bmax, top, frh, val, topSort, quiet, response, avgPayout, paidAllSort sql.NullFloat64
		var rewarded, paidAccepted, counted, totalReports, hunters, acceptedAll sql.NullInt64
		var paid90Sort, avgLow, avgSort sql.NullFloat64
		var invited, joined, listed sql.NullString
		var points90, pointsAll, hunters90 sql.NullInt64
		dest := []any{&it.ID, &it.Platform, &it.Handle, &it.Name, &it.URL, &it.Private, &it.State, &it.OffersBounty,
			&started, &firstSeen, &removed, &changed, &it.InScope, &it.OutOfScope, &it.Wildcards, &it.Domains,
			&captured, &reports, &valid, &earned, &paid90, &paidAll, &reports90, &it.StatsCurrency, &it.Credentials, &it.Disclosed, &resumed, &hunt, &it.Ignored, &tags}
		dest = append(dest, it.Requirements.scanTargets()...)
		dest = append(dest, &it.Currency, &rewards, &bmin, &bmax, &top, &r7, &rm, &acc, &frh, &val,
			&it.Fresh, &res90, &it.Resolved90dEstimated, &acceptedAll, &it.Apps, &it.APIs, &topSort, &quiet, &response, &rewarded, &avgPayout, &paidAllSort, &paidAccepted, &paid90Sort, &avgLow, &avgSort, &counted, &totalReports, &hunters,
			&it.InvitePending, &invited, &joined, &points90, &pointsAll, &hunters90, &listed)
		if err := rows.Scan(dest...); err != nil {
			return nil, 0, err
		}
		if rewards.Valid {
			if err := json.Unmarshal([]byte(rewards.String), &it.Rewards); err != nil {
				return nil, 0, fmt.Errorf("program %d rewards: %w", it.ID, err)
			}
		}
		it.BountyMin, it.BountyMax, it.BountyFrom = targetBounty(it.Rewards, "", floatPtr(bmin), floatPtr(bmax), floatPtr(top))
		it.Reports7d, it.ReportsMonth, it.Accepted90d, it.Resolved90d = intPtr(r7), intPtr(rm), intPtr(acc), intPtr(res90)
		it.FirstResponseHours, it.ValidationHours = floatPtr(frh), floatPtr(val)
		var err error
		if it.Hunt, err = parseHunt(hunt); err != nil {
			return nil, 0, err
		}
		it.Paid90d, it.PaidAll, it.Reports90d = floatPtr(paid90), floatPtr(paidAll), intPtr(reports90)
		it.Rewarded, it.AvgPayout, it.AvgBountyLow = intPtr(rewarded), floatPtr(avgPayout), floatPtr(avgLow)
		it.CountedDays, it.TotalReports, it.Hunters = intPtr(counted), intPtr(totalReports), intPtr(hunters)
		it.AcceptedAll = intPtr(acceptedAll)
		it.Points90d, it.PointsAll, it.Hunters90d = intPtr(points90), intPtr(pointsAll), intPtr(hunters90)
		if it.PaidAll == nil && paidAllSort.Valid {
			it.PaidAll, it.PaidAllEstimated = floatPtr(paidAllSort), true
		}
		if it.Paid90d == nil && paid90Sort.Valid {
			it.Paid90d, it.Paid90dEstimated, it.PaidAccepted90d = floatPtr(paid90Sort), true, intPtr(paidAccepted)
		}
		it.Tags = splitTags(tags)
		it.StartedAt = parseTSPtr(started)
		it.FirstSeenAt = parseTS(firstSeen)
		it.RemovedAt = parseTSPtr(removed)
		it.ScopeChangedAt = parseTSPtr(changed)
		it.ResumedAt = parseTSPtr(resumed)
		it.InvitedAt, it.JoinedAt, it.ListedAt = parseTSPtr(invited), parseTSPtr(joined), parseTSPtr(listed)
		if captured.Valid {
			it.Stats = &UserStatsSnapshot{CapturedAt: parseTS(captured.String), Reports: int(reports.Int64),
				ValidReports: int(valid.Int64), BountyEarned: earned.Float64}
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

// OpenHandle is one open program's handle on a platform, for the calls that need
// an example rather than a list (a page to read, a form to look at). Empty when
// the platform has no open program.
func (s *Store) OpenHandle(ctx context.Context, platform string) (string, error) {
	var handle string
	err := s.db.R.QueryRowContext(ctx, `SELECT handle FROM programs
		WHERE platform = ? AND state = ? AND removed_at IS NULL AND ignored = 0
		ORDER BY hunt_score DESC, handle LIMIT 1`, platform, "open").Scan(&handle)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open handle: %w", err)
	}
	return handle, nil
}
