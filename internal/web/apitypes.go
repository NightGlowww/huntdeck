package web

import (
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// Bodies of the JSON API's responses that the handlers build here (the
// program, stats and report types sit with the code that fills them).
// openapi.go turns every type a response uses into the spec's schemas, so
// changing a field here changes /api/v1/openapi.json and the API page too.

// apiErrorJSON is every error: a sentence for people, not a code.
type apiErrorJSON struct {
	Error string `json:"error"`
}

// apiOKJSON is /healthz's answer ("ok" or "unavailable") and a started
// poll's ("started").
type apiOKJSON struct {
	Status string `json:"status"`
}

type apiStatusJSON struct {
	Version string `json:"version"`
	// ReadOnly says this copy cannot be changed (a demo or a published copy):
	// a client can hide its write actions instead of meeting a 403.
	ReadOnly  bool                `json:"read_only"`
	Platforms []apiStatusPlatform `json:"platforms"`
	LastPolls []apiStatusRun      `json:"last_polls"`
}

type apiStatusPlatform struct {
	Platform        string         `json:"platform"`
	Programs        int            `json:"programs"`
	Private         int            `json:"private"`
	Open            int            `json:"open"`
	Bounty          int            `json:"bounty"`
	Removed         int            `json:"removed"`
	InScopeEntries  int            `json:"in_scope_entries"`
	OutScopeEntries int            `json:"out_of_scope_entries"`
	Targets         map[string]int `json:"in_scope_targets"` // by kind
	// CheckedAt is when the platform's listing was last read (its latest
	// poll, or on a copy loaded from a seed, when its programs were last
	// seen listed).
	CheckedAt *time.Time `json:"checked_at"`
}

type apiStatusRun struct {
	Platform     string     `json:"platform"`
	Mode         string     `json:"mode"`
	Status       string     `json:"status"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	ProgramsSeen int        `json:"programs_seen"`
	Changes      int        `json:"changes"`
	Requests     int64      `json:"requests"`
	Error        string     `json:"error,omitempty"`
	// Progress is set only while the run is running: the fields above arrive
	// when it ends.
	Progress *apiRunProgress `json:"progress,omitempty"`
}

type apiProgramListJSON struct {
	Total    int              `json:"total"`
	Programs []apiProgramItem `json:"programs"`
}

// apiProgramItem is a program as the Programs list has it.
type apiProgramItem struct {
	apiProgramRef
	Invite         *apiInvite        `json:"invite,omitempty"` // private programs only
	OffersBounty   bool              `json:"offers_bounty"`
	FirstSeenAt    time.Time         `json:"first_seen_at"`
	RemovedAt      *time.Time        `json:"removed_at,omitempty"`
	InScope        int               `json:"in_scope_entries"`
	OutOfScope     int               `json:"out_of_scope_entries"`
	Wildcards      int               `json:"wildcards"`
	Domains        int               `json:"domains"`
	StartedAt      *time.Time        `json:"started_at,omitempty"`
	ScopeChangedAt *time.Time        `json:"scope_changed_at,omitempty"`
	Stats          *apiYourStats     `json:"your_stats,omitempty"`
	Credentials    bool              `json:"test_credentials"`
	Requirements   *apiRequirements  `json:"requirements"`
	Disclosed      int               `json:"disclosed_reports"`
	Hunt           *store.HuntDetail `json:"hunt_score"`
	Ignored        bool              `json:"ignored"`
	Tags           []string          `json:"tags"`
	Bounty         *apiBounty        `json:"bounty"`
	NewScope30d    int               `json:"new_scope_30d"`
	Apps           int               `json:"apps"`
	APIs           int               `json:"apis"`
	ResumedAt      *time.Time        `json:"resumed_at,omitempty"` // last went from paused or closed to open
	Numbers        apiProgramNumbers `json:"numbers"`
	Trends         *apiProgramTrends `json:"trends,omitempty"` // with trends=1
	apiProgramFreshness
}

// apiProgramFreshness is when a program last changed and was last
// checked.
type apiProgramFreshness struct {
	// UpdatedAt is the last time anything HuntDeck keeps of the program
	// changed: first seen, a recorded change (scope, policy, status,
	// bounty table…), one of its numbers, or its removal.
	UpdatedAt time.Time `json:"updated_at"`
	// LastSyncedAt is the last time HuntDeck read the program from its
	// platform, whether anything changed or not; null for programs added
	// by hand.
	LastSyncedAt *time.Time `json:"last_synced_at"`
}

// apiInvite is the user's invitation to a private program: pending until
// accepted on the platform (where it lists pending invitations: Intigriti),
// when it was sent and when it was accepted, where known.
type apiInvite struct {
	Pending   bool       `json:"pending"`
	InvitedAt *time.Time `json:"invited_at,omitempty"`
	JoinedAt  *time.Time `json:"joined_at,omitempty"`
}

// apiProgramNumbers are the Programs page's columns, in currency; null
// where the platform doesn't publish the number. An estimate (worked out
// from what the platform does publish; see the Methodology page) is
// flagged.
type apiProgramNumbers struct {
	Currency             string   `json:"currency"`
	Paid90d              *float64 `json:"paid_90d"`
	Paid90dEstimated     bool     `json:"paid_90d_estimated"` // an estimate by how it's worked out, or counted from under 90 days of saved totals
	PaidTotal            *float64 `json:"paid_total"`
	PaidTotalEstimated   bool     `json:"paid_total_estimated"`
	AvgBountyLow         *float64 `json:"average_bounty_low"`
	AvgBountyHigh        *float64 `json:"average_bounty_high"`
	RewardedReports      *int     `json:"rewarded_reports"`
	Received90d          *int     `json:"reports_received_90d"`
	Received90dEstimated bool     `json:"reports_received_90d_estimated"` // counted from under 90 days of saved totals
	CountedDays          *int     `json:"reports_received_counted_days"`  // worked out from this many days of saved totals; under 90 is an estimate
	Resolved90d          *int     `json:"resolved_90d"`
	Resolved90dEstimated bool     `json:"resolved_90d_estimated"`
	Accepted90d          *int     `json:"accepted_90d"`
	Reports7d            *int     `json:"reports_7d"`
	ReportsThisMonth     *int     `json:"reports_this_month"`
	TotalReports         *int     `json:"total_reports"`
	AcceptedTotal        *int     `json:"accepted_total"`
	Hunters              *int     `json:"hunters"`
	Hunters90d           *int     `json:"hunters_90d"` // researchers who earned leaderboard points in 90 days (Intigriti); 100 means 100 or more
	FirstResponseHours   *float64 `json:"first_response_hours"`
	ValidationHours      *float64 `json:"validation_hours"`
}

// apiProgramTrends are the Programs page's 90-day lines, a point a day.
type apiProgramTrends struct {
	Received []apiDayValue `json:"reports_received_90d"`
	Paid     []apiDayValue `json:"paid_90d"`
	Resolved []apiDayValue `json:"resolved_90d"`
}

// apiProgramJSON is one program in full: its scope, public stats and
// reports.
type apiProgramJSON struct {
	apiProgramRef
	Invite             *apiInvite      `json:"invite,omitempty"` // private programs only
	OffersBounty       bool            `json:"offers_bounty"`
	Currency           string          `json:"currency,omitempty"`
	FastPayments       bool            `json:"fast_payments"`
	SafeHarbor         bool            `json:"safe_harbor"`
	StartedAt          *time.Time      `json:"started_at,omitempty"`
	JoinedAt           *time.Time      `json:"joined_at,omitempty"`
	FirstSeenAt        time.Time       `json:"first_seen_at"`
	LastSeenAt         time.Time       `json:"last_seen_at"`
	RemovedAt          *time.Time      `json:"removed_at,omitempty"`
	ScopeSyncedAt      *time.Time      `json:"scope_synced_at,omitempty"`
	ScopeCheckedAt     *time.Time      `json:"scope_checked_at,omitempty"`
	ScopeUnavailableAt *time.Time      `json:"scope_unavailable_at,omitempty"`
	Stats              *apiYourStats   `json:"your_stats,omitempty"`
	PublicStats        *apiPublicStats `json:"program_stats,omitempty"`
	Scope              []apiScopeEntry `json:"scope"`
	// ScopeTotal is every entry the program has, however many this answer
	// carries: with ?scope_limit and ?scope_offset they page it.
	ScopeTotal int                `json:"scope_total,omitempty"`
	Disclosed  *disclosedJSON     `json:"disclosed_reports,omitempty"`
	Resolved   *resolvedJSON      `json:"resolved_reports,omitempty"`
	Accepted   *acceptedJSON      `json:"accepted_reports,omitempty"`
	Hunt       *store.HuntDetail  `json:"hunt_score"`
	Ignored    bool               `json:"ignored"`
	Tags       []string           `json:"tags"`
	Bounty     *apiBounty         `json:"bounty"` // as the Programs list has it
	Numbers    *apiProgramNumbers `json:"numbers"`
	Trends     *apiProgramTrends  `json:"trends"`
	// Rules are what "before you test" shows: the platform's requirements and
	// what only the policy text states (a header, a rate, what is forbidden).
	Rules []apiTestRule `json:"rules,omitempty"`
	// AIRules are what a model read out of the same policy, when one ran: a
	// paraphrase to check against Rules, which quote the policy itself.
	AIRules         []store.AIRule    `json:"ai_rules,omitempty"`
	AIModel         string            `json:"ai_model,omitempty"`
	AIAt            *time.Time        `json:"ai_read_at,omitempty"`
	Requirements    *apiRequirements  `json:"requirements"`
	TestCredentials []string          `json:"test_credentials"` // scope the platform hands out test credentials for
	Testing         *apiTestingRules  `json:"testing_rules"`
	Facts           apiProgramFacts   `json:"facts"`
	Brief           apiProgramBrief   `json:"brief"`
	Announcements   []apiAnnouncement `json:"announcements"`
	apiProgramFreshness
}

// apiScopeEntry is one scope entry as the platform lists it, with the
// targets HuntDeck reads from it.
type apiScopeEntry struct {
	InScope        bool             `json:"in_scope"`
	AssetType      string           `json:"asset_type"`
	PlatformType   string           `json:"platform_asset_type"`
	Identifier     string           `json:"identifier"`
	BountyEligible bool             `json:"bounty_eligible"`
	Credentials    bool             `json:"test_credentials"`
	MaxSeverity    string           `json:"max_severity,omitempty"`
	Instruction    string           `json:"instruction,omitempty"`
	FirstSeenAt    time.Time        `json:"first_seen_at"`
	AddedAt        *time.Time       `json:"added_at,omitempty"`
	RemovedAt      *time.Time       `json:"removed_at,omitempty"`
	Targets        []apiScopeTarget `json:"targets"`
}

type apiScopeTarget struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type apiStatsListJSON struct {
	Sort     string          `json:"sort"`
	Total    int             `json:"total"`
	Totals   []apiStatsTotal `json:"totals"` // by currency
	Programs []apiStatsRow   `json:"programs"`
}

// apiStatsTotal is the programs whose amounts are in one currency, their
// 90-day figures added up as the Stats page adds them (/api/v1/stats/platforms):
// the Programs list's figures, Bugcrowd's estimates included; money from
// programs that pay bounties only.
type apiStatsTotal struct {
	Currency           string  `json:"currency"`
	Programs           int     `json:"programs"` // every program the filter matches, with stats or not
	BountiesPaid90d    float64 `json:"bounties_paid_90d"`
	PaidPrograms       int     `json:"bounties_paid_90d_programs"` // how many programs that adds up
	PaidEstimated      bool    `json:"bounties_paid_90d_estimated"`
	ReportsReceived90d int     `json:"reports_received_90d"`
	ReceivedPrograms   int     `json:"reports_received_90d_programs"`
	ReceivedEstimated  bool    `json:"reports_received_90d_estimated"`
	BountiesPaidTotal  float64 `json:"bounties_paid_total"`
	PaidTotalEstimated bool    `json:"bounties_paid_total_estimated"`
}

type apiStatsRow struct {
	apiProgramRef
	OffersBounty  bool            `json:"offers_bounty"`
	Wildcards     int             `json:"wildcards"`
	PaidPerReport *float64        `json:"bounties_paid_per_report_90d"`
	Stats         *apiPublicStats `json:"program_stats"`
}

type apiTargetListJSON struct {
	Total   int            `json:"total"`
	Limit   int            `json:"limit"`
	Offset  int            `json:"offset"`
	Targets []apiTargetRow `json:"targets"`
}

type apiTargetRow struct {
	Kind    string        `json:"kind"`
	Value   string        `json:"value"`
	InScope bool          `json:"in_scope"`
	Bounty  bool          `json:"bounty_eligible"`
	AddedAt *time.Time    `json:"added_at,omitempty"`
	Program apiProgramRef `json:"program"`
	Score   *float64      `json:"score,omitempty"`   // sort=best only
	Reasons []string      `json:"reasons,omitempty"` // sort=best only
	// NewToYou (sort=best only): in scope when you got access to its
	// private program, added_at being when you did.
	NewToYou bool `json:"new_to_you,omitempty"`
}

type apiChangeListJSON struct {
	Total   int         `json:"total"`
	Changes []apiChange `json:"changes"`
}

type apiChange struct {
	ID          int64         `json:"id"`
	OccurredAt  time.Time     `json:"occurred_at"`
	Kind        string        `json:"kind"`
	Field       string        `json:"field,omitempty"`
	OldValue    string        `json:"old_value,omitempty"`
	NewValue    string        `json:"new_value,omitempty"`
	Description string        `json:"description"`
	Program     apiProgramRef `json:"program"`
}

type apiDisclosedListJSON struct {
	Total   int               `json:"total"`
	Reports []apiDisclosedRow `json:"reports"`
}

type apiDisclosedRow struct {
	disclosureJSON
	Currency string        `json:"currency,omitempty"`
	Program  apiProgramRef `json:"program"`
}

// apiMatchJSON is one program's verdict on a host, and the rule that
// decided it (or lists it).
type apiMatchJSON struct {
	InScope bool          `json:"in_scope"`
	Via     string        `json:"via"`
	Kind    string        `json:"via_kind"`
	Program apiProgramRef `json:"program"`
}

type apiPastMatch struct {
	apiMatchJSON
	Identifier string    `json:"identifier"`
	RemovedAt  time.Time `json:"removed_at"`
}

type apiHostMatchJSON struct {
	Host    string         `json:"host"`
	Matches []apiMatchJSON `json:"matches"`
	// Past are matches against scope programs have removed.
	Past []apiPastMatch `json:"past"`
	// Under is live scope at or under the host that doesn't decide it:
	// wildcards on the host itself (they cover its subdomains, not it) and
	// its listed subdomains, the first 50 of UnderTotal.
	Under      []apiMatchJSON `json:"under"`
	UnderTotal int            `json:"under_total"`
	// Mentioning, when nothing decides the host or is under it, is live
	// scope whose target contains it ("canva-apps" finds *.canva-apps.cn),
	// the first 50 of MentioningTotal.
	Mentioning      []apiMatchJSON `json:"mentioning"`
	MentioningTotal int            `json:"mentioning_total"`
}

type apiBulkMatchJSON struct {
	In      int             `json:"in"`
	Out     int             `json:"out"`
	None    int             `json:"none"`
	Results []apiBulkResult `json:"results"`
}

type apiBulkResult struct {
	Input   string         `json:"input"`
	Verdict string         `json:"verdict"` // in, out or none
	Matches []apiMatchJSON `json:"matches"`
}

// apiQueueJSON is HuntQueue: the ranked targets and when they were ranked.
type apiQueueJSON struct {
	ComputedAt string        `json:"computed_at"`
	Rows       []apiQueueRow `json:"rows"`
	// Gated are targets the queue left out, with the reason: what is one
	// invitation or one requirement away is still worth knowing.
	Gated []apiQueueRow `json:"gated,omitempty"`
}

type apiQueueRow struct {
	Platform string `json:"platform"`
	Handle   string `json:"handle"`
	Kind     string `json:"kind"`
	Value    string `json:"value"`
	Apex     string `json:"apex,omitempty"`
	// Factors is the score breakdown, returned when the request asks for it.
	Factors []apiQueueFactor `json:"factors,omitempty"`
	Score   float64          `json:"score"`
	Reasons []string         `json:"reasons"`
	Gate    string           `json:"gate,omitempty"`
}

// apiTestRule is one thing to know before testing a program.
type apiTestRule struct {
	Kind       string `json:"kind"` // req, auto, rate, email, header, agent, creds, warn
	Title      string `json:"title"`
	Detail     string `json:"detail,omitempty"`
	Copy       string `json:"copy,omitempty"`        // a value to send, verbatim
	Alert      bool   `json:"alert,omitempty"`       // limits how you may test
	FromPolicy bool   `json:"from_policy,omitempty"` // read out of the policy text
}

// apiQueueFactor is one input's part in a queue score: the value it took and
// the multiplier it applied — the multipliers of a row multiply back to the
// score, so the biggest loss is the one to look at.
type apiQueueFactor struct {
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Weight float64 `json:"weight"`
	Effect float64 `json:"effect"`
}

// apiRunProgress is what a running poll is doing right now. The stored row only
// carries the totals, and they are written when the run ends: a poll two minutes
// into a ten-minute listing reads as zero requests, which looks like a hang and
// cost an hour of diagnosis once.
type apiRunProgress struct {
	Step        string `json:"step"`
	Done        int    `json:"done"`
	Total       int    `json:"total"`
	Requests    int64  `json:"requests"`
	IdleSeconds int    `json:"idle_seconds"`
}
