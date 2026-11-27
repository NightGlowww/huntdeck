// Package platform defines the interface every bug bounty platform
// integration implements, and the plain data types they return.
//
// Platform packages only talk to the platform. They never touch the
// database: internal/syncer persists results and computes changes.
package platform

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Platform lists programs and fetches their scope.
type Platform interface {
	// Name is the short identifier stored in the database, e.g. "hackerone".
	Name() string

	// ListPrograms returns every program the account can see. If any page
	// fails, it returns what it has with Complete=false and a non-nil error.
	ListPrograms(ctx context.Context) (Listing, error)

	// FetchScope returns the full current scope of one program. An error
	// means the scope is unknown; callers must not treat it as empty.
	FetchScope(ctx context.Context, p Program) ([]ScopeEntry, error)

	// Stats reports HTTP totals for the poll-run record.
	Stats() (requests, rateLimited int64)
}

// ErrScopeUnavailable is returned (wrapped) by FetchScope when the platform
// deliberately withholds a program's scope, e.g. until the user accepts its
// terms. It isn't a failure: the previous scope, if any, is kept.
var ErrScopeUnavailable = errors.New("scope not available")

// ErrNoWebLogin is wrapped by errors for logged-in website data asked of a
// platform set up without its website login: nothing to report.
var ErrNoWebLogin = errors.New("no website login configured")

// ScopeDetailFetcher is implemented by platforms whose scope download also
// carries the program's rules and bounty table. Polls call it instead of
// FetchScope, which then returns the same entries.
type ScopeDetailFetcher interface {
	FetchScopeDetail(ctx context.Context, p Program) ([]ScopeEntry, ScopeDetail, error)
}

// ScopeDetail is what a scope download says about the program besides its
// scope.
type ScopeDetail struct {
	Policy  string      // the rules, Markdown; "" when the response has none
	Rewards []RewardRow // the bounty table: nil when the response doesn't carry one
	// Testing is the program's technical rules for testing; nil when the
	// response doesn't carry them.
	Testing *TestingRules
	// Facts are more program facts the brief carries; nil when it has none.
	Facts *Facts
	// Requirements are what the brief says a hacker needs; nil when it
	// doesn't say.
	Requirements *Requirements
	// State is the program's state as the brief shows it (StateOpen,
	// StatePaused, StateClosed), "" where it doesn't say; StateSince when
	// the brief version showing it was published, if known.
	State      string
	StateSince *time.Time
}

// TestingRules are what a program asks of test traffic.
type TestingRules struct {
	RateLimit     float64 // requests per second allowed; 0 when not given
	NoAutomation  bool    // automated tools aren't allowed at all
	UserAgent     string  // a User-Agent to send; "" when not required
	Header        string  // a request header to send, "Name: value"; "" when not required
	PlatformEmail bool    // test accounts must use the platform's email alias (e.g. @intigriti.me)
}

// ScopeChangeChecker is implemented by platforms that can tell when a
// program's scope last changed more cheaply than fetching it. Full polls ask
// it for programs whose listing doesn't say, and skip the fetch when the
// scope hasn't changed since the last one.
type ScopeChangeChecker interface {
	// ScopeChangedAt returns when p's scope last changed, or the zero time
	// if the platform doesn't say.
	ScopeChangedAt(ctx context.Context, p Program) (time.Time, error)
}

// ScopeHistorian is implemented by platforms that keep past versions of a
// program's scope, so entries can be dated from before HuntDeck tracked
// them.
type ScopeHistorian interface {
	// ScopeHistory reads the scope versions of each program and calls save
	// with them, oldest first, after each program. Programs it can't read
	// are skipped without a save; it stops at the first error that would
	// affect the rest (a block, an expired session).
	ScopeHistory(ctx context.Context, programs []Program, save ScopeHistorySaver) error
}

// ScopeHistorySaver receives one program's scope versions, oldest first.
type ScopeHistorySaver func(handle string, versions []ScopeVersion) error

// ScopeVersion is a program's scope as published at At, until the next
// version. Histories may skip versions that didn't change the scope.
type ScopeVersion struct {
	At      time.Time
	Entries []ScopeEntry
}

// RecentLister is implemented by platforms whose listing can be read
// newest program first, so quick polls can read one page of it between
// complete listings to spot new programs.
type RecentLister interface {
	// ListRecent returns the most recently launched programs (one page).
	// The listing is never Complete: nothing may be inferred removed from it.
	ListRecent(ctx context.Context) (Listing, error)
}

// StateHistorian is implemented by platforms whose feeds say when programs
// were paused, resumed or closed, so state changes can be dated from before
// HuntDeck tracked them.
type StateHistorian interface {
	// StateHistory calls save with each program's past state changes,
	// oldest first. Programs without any are skipped.
	StateHistory(ctx context.Context, programs []Program, save StateHistorySaver) error
}

// StateHistorySaver receives one program's state changes, oldest first.
type StateHistorySaver func(handle string, changes []StateChange) error

// StateChange is a program going from one state (StateOpen, StatePaused,
// StateClosed) to another at At.
type StateChange struct {
	At       time.Time
	From, To string
}

// InviteLister is implemented by platforms that list the invitations to
// private programs the user hasn't accepted yet.
type InviteLister interface {
	// ListInvites returns every pending invitation. An error means they
	// weren't read this time (a login that needs a browser, say): nothing
	// may be inferred from it.
	ListInvites(ctx context.Context) ([]Invite, error)
}

// InviteAccepter is implemented by platforms that can accept a pending
// invitation for the user (Intigriti, HackerOne and Bugcrowd websites).
type InviteAccepter interface {
	// AcceptInvite accepts the invitation to the program with this handle.
	AcceptInvite(ctx context.Context, handle string) error
}

// Invite is an invitation to a private program, not yet accepted.
type Invite struct {
	Handle    string    // the program's, as in its listing
	InvitedAt time.Time // when it was sent; zero if not known
}

// DisclosureFetcher is implemented by platforms that publish disclosed
// reports (HackerOne's Hacktivity).
type DisclosureFetcher interface {
	// FetchDisclosures pages through publicly disclosed reports, newest
	// disclosure first: every program's if p is nil, else only p's. It
	// calls save with each page and stops when save says so, at the last
	// page, or after maxPages.
	FetchDisclosures(ctx context.Context, p *Program, maxPages int, save DisclosureSaver) error
}

// DisclosureSaver receives one page of disclosures and says whether to
// fetch the next.
type DisclosureSaver func(page []Disclosure) (more bool, err error)

// Disclosure is one publicly disclosed report: what the platform lists
// about it and a link, never the write-up.
type Disclosure struct {
	ID            string // the platform's report ID
	ProgramHandle string
	Title         string
	Severity      string // "none", "low", "medium", "high", "critical", or "" if unrated
	Weakness      string // CWE name
	CVEIDs        []string
	Substate      string   // e.g. "resolved", "informative"
	Bounty        *float64 // total awarded, in the program's currency, when public
	SubmittedAt   *time.Time
	DisclosedAt   time.Time
	Votes         int
	URL           string
	Reporter      string // username
	Summary       string // the platform's short summary
	Raw           json.RawMessage
}

// ResolutionFetcher is implemented by platforms that list resolved reports
// that aren't disclosed (HackerOne's Hacktivity): no title or details, but
// when each was submitted and resolved, which shows how a program is doing.
type ResolutionFetcher interface {
	// FetchResolutions pages through resolved, undisclosed reports, latest
	// activity first: every program's if p is nil, else only p's. It calls
	// save with each page and stops when save says so, at the last page, or
	// after maxPages.
	FetchResolutions(ctx context.Context, p *Program, maxPages int, save ResolutionSaver) error
}

// ResolutionSaver receives one page of resolutions; see ResolutionFetcher.
type ResolutionSaver func(page []Resolution) (more bool, err error)

// Resolution is a resolved report the platform lists without disclosing it.
type Resolution struct {
	ID            string // the platform's report ID
	ProgramHandle string
	SubmittedAt   *time.Time
	// ActivityAt is the report's latest public action: its resolution, or a
	// bounty or swag awarded after it (so the report was resolved by then).
	ActivityAt time.Time
	Action     string   // "resolved", "bounty" or "swag"
	Bounty     *float64 // total awarded, in the program's currency, when shown
	Reporter   string
}

// AcceptedFetcher is implemented by platforms that list accepted (valid)
// reports without their details (Bugcrowd's CrowdStream): when each was
// submitted, accepted and resolved, which shows how many valid reports a
// program gets and how fast it fixes them.
type AcceptedFetcher interface {
	// FetchAccepted pages through accepted reports, latest accepted first:
	// every program's if p is nil, else only p's. It calls save with each
	// page and stops when save says so, at the end of the feed's complete
	// window, or after maxPages.
	FetchAccepted(ctx context.Context, p *Program, maxPages int, save AcceptedSaver) error
}

// AcceptedSaver receives one page of accepted reports; see AcceptedFetcher.
type AcceptedSaver func(page AcceptedPage) (more bool, err error)

// AcceptedPage is one page of an accepted-reports feed.
type AcceptedPage struct {
	Reports []AcceptedReport
	// Since is where the feed's complete window starts: every report
	// accepted since then is listed. Zero when the feed doesn't say.
	Since time.Time
}

// AcceptedReport is a report the platform lists as accepted, without
// disclosing it.
type AcceptedReport struct {
	ID            string // the platform's report ID
	ProgramHandle string
	SubmittedAt   *time.Time
	AcceptedAt    time.Time  // a day, at 00:00 UTC, when the platform gives only the day
	ResolvedAt    *time.Time // nil while unresolved
	Priority      int        // 1 (critical) to 5 (informational); 0 if not given
	Bounty        *float64   // when shown, in the program's currency
	Reporter      string
}

// ThanksFetcher is implemented by platforms that list each program's
// hunters with how many of their reports it accepted (YesWeHack's Thanks):
// added up, a program's valid reports, all time.
type ThanksFetcher interface {
	// FetchThanks reads handle's list, taken to put the hunters whose
	// count last changed first. Given known (hunter → valid reports, from
	// the last read), it stops at the first page that matches known
	// throughout; given nil, it reads every page.
	FetchThanks(ctx context.Context, handle string, known map[string]int) (Thanks, error)
}

// Thanks is what FetchThanks read of a program's list.
type Thanks struct {
	Shown    bool           // false: the program doesn't show a list
	Hunters  map[string]int // the hunters read, with their valid reports
	Listed   int            // how many hunters the list has
	Complete bool           // every page was read
}

// Resolution actions.
const (
	ActionResolved = "resolved"
	ActionBounty   = "bounty"
	ActionSwag     = "swag"
)

// AccountNamer is implemented by platforms that can say which account
// they list programs for: a stable key for it such as the username or
// email, "" if they can't tell. The syncer keeps a hash of it, so another
// account's private programs count as already the user's when the account
// changes, rather than as invitations just received.
type AccountNamer interface {
	Account() string
}

// KeepAliver is implemented by platforms whose login session expires when
// idle. The server calls KeepAlive periodically between polls.
type KeepAliver interface {
	KeepAlive(ctx context.Context) error
}

// WebSyncer is implemented by platforms whose logged-in website has program
// data the regular polls can't get. A separate browser sync reads it on its
// own schedule (Settings, "Browser syncs"), and while it's on, full polls
// leave the platform's program stats to it.
type WebSyncer interface {
	// WebSyncConfigured reports whether a web login is set up.
	WebSyncConfigured() bool
	// WebSync fetches program stats from the logged-in site, as
	// StatsFetcher.FetchProgramStats does (falling back to whatever the
	// polls would get if the login fails, and returning the login's error).
	WebSync(ctx context.Context, handles []string, save StatsSaver) error
	// WebSession describes the login, for Settings.
	WebSession() WebSession
	// WebSyncPulls says, in plain words, what the sync reads on each program.
	WebSyncPulls() []string
}

// WebSession is the state of a platform's web login.
type WebSession struct {
	SignedIn         bool       // a session is held now
	LastBrowserLogin *time.Time // when the browser last logged in
	LastRenewal      *time.Time // when the session was last renewed without the browser
	LastFailure      *time.Time
	Failure          string     // the last login failure; "" once a login works
	RetryAfter       *time.Time // the next login isn't tried before this
	Stuck            bool       // the credentials were refused: no retries until a restart
}

// StatsFetcher is implemented by platforms that publish statistics about
// their programs (bounties paid, reports resolved, response times).
type StatsFetcher interface {
	// FetchProgramStats fetches statistics for handles in batches and calls
	// save after each one with the handles it covered and the stats found,
	// keyed by handle (programs the platform has nothing for are missing).
	// Saving per batch means a crash loses at most one batch. It stops at the
	// first error, from the platform or from save.
	FetchProgramStats(ctx context.Context, handles []string, save StatsSaver) error
}

// StatsSaver receives one batch of program stats; see StatsFetcher.
type StatsSaver func(covered []string, stats map[string]ProgramStats) error

// LeaderboardHistorian is implemented by platforms whose program
// leaderboards can be read for past months (Intigriti), so accepted reports
// per month can be estimated from before HuntDeck saved a program's totals.
type LeaderboardHistorian interface {
	// LeaderboardMonths reads each program's leaderboard for the months
	// asked and calls save after each program. A program it can't read is
	// skipped without a save; it stops at the first error that would
	// affect the rest (a session it can't use).
	LeaderboardMonths(ctx context.Context, want []LeaderboardWant, save LeaderboardSaver) error
}

// LeaderboardWant is a program and the months of its leaderboard to read,
// each the first of the month, UTC.
type LeaderboardWant struct {
	Handle string
	Months []time.Time
}

// LeaderboardMonth is one month of a program's leaderboard in sums: the
// points researchers earned on it (nil where the program hides them), how
// many earned any, and whether the board was cut short (LeaderboardCap).
type LeaderboardMonth struct {
	Month   time.Time
	Points  *int
	Hunters int
	Capped  bool
}

// LeaderboardSaver receives one program's months; see LeaderboardHistorian.
type LeaderboardSaver func(handle string, months []LeaderboardMonth) error

// LeaderboardCap is the most researchers a program leaderboard lists
// (Intigriti, 2026-11): a board this long may be missing some.
const LeaderboardCap = 100

// ProgramStats are a program's public statistics as the platform shows
// them. Nil means the platform doesn't show that number for the program.
// Amounts are in Currency.
type ProgramStats struct {
	Currency            string
	ResolvedReports     *int
	ReportsLast90d      *int
	BountiesPaid        *float64
	BountiesPaidLast90d *float64
	AvgBountyLow        *float64 // average bounty, shown as a range
	AvgBountyHigh       *float64
	TopBountyLow        *float64 // largest bounty, shown as a range
	TopBountyHigh       *float64
	ResponseEfficiency  *int // percent of reports answered within the program's targets
	LastResolvedAt      *time.Time
	HackersThanked      *int
	// Rewards is the bounty table, one row per asset or group: nil when the
	// stats don't carry one, empty when the program has none.
	Rewards           []RewardRow
	SignalRequirement *float64 // minimum reputation signal to submit
	RewardedReports   *int     // reports that earned a bounty
	TotalReports      *int     // every report submitted, valid or not
	ReportsLast7d     *int     // reports submitted in the last 7 days
	ReportsThisMonth  *int     // reports submitted this calendar month so far
	AcceptedReports   *int     // reports accepted as valid, all time
	// Hunters counts the researchers on the program, as the platform does:
	// Bugcrowd those who joined it, YesWeHack those ranked on it, Intigriti
	// those who contributed (HackerOne's is HackersThanked).
	Hunters *int
	// Points90d and PointsAll are the reputation points researchers earned
	// on the program in the last 90 days and all time, from its leaderboards
	// (Intigriti): nil unless both were read whole. Hunters90d counts the
	// researchers who earned points in the last 90 days (LeaderboardCap
	// means that many or more).
	Points90d, PointsAll, Hunters90d *int
	LastSubmissionAt                 *time.Time // the program's latest report, by anyone
	LastAcceptedAt                   *time.Time // when a report was last accepted, if recent enough to show
	ScopeRank                        *int       // Bugcrowd's own rating of the scope, 1–4
	// KnownIssues and KnownIssuesUnique are the program's known issues, all
	// and distinct (Bugcrowd): findings already reported, so a new one
	// matching them is a duplicate.
	KnownIssues, KnownIssuesUnique *int
	// Announcements are the program's posts to researchers (Bugcrowd),
	// newest first; nil when they weren't read.
	Announcements []Announcement

	// CountedDays is set by the syncer, not platforms: where the platform
	// publishes only running totals, ReportsLast90d and BountiesPaidLast90d
	// are worked out from the totals HuntDeck saved, and this is how many
	// days of them there were (90 or more: counted; fewer: estimates).
	CountedDays *int
	// RewardedLast90d is set by the syncer too: reports rewarded in the last
	// 90 days, from the saved RewardedReports, where it works paid in 90
	// days out from them (Bugcrowd programs that keep CrowdStream private).
	RewardedLast90d *float64

	// Average hours from submission to each step, as the program page shows.
	FirstResponseHours *float64
	TriageHours        *float64
	BountyHours        *float64
	ResolutionHours    *float64
	// ValidationHours is the time within which most submissions are
	// accepted or rejected (Bugcrowd: 75% of them).
	ValidationHours *float64

	// BySeverity is the average bounty and resolved report count per
	// severity, the basis for "% of submissions".
	BySeverity SeverityStats

	// Requirements are what a hacker needs to submit, if the stats carry them.
	Requirements *Requirements
	// Facts are more program facts the stats carry; nil when they have none.
	Facts *Facts

	// Profile is the program's descriptive text. Unlike the numbers it isn't
	// kept per day: the latest replaces the previous.
	Profile ProgramProfile
	// Archived are scope entries the program has removed, with when. They
	// give scope history from before HuntDeck tracked it.
	Archived []ArchivedScope
}

// Announcement is a program's post to researchers.
type Announcement struct {
	ID          string
	Title       string
	Body        string // Markdown
	PublishedAt time.Time
}

// ArchivedScope is a scope entry a program removed, and when.
type ArchivedScope struct {
	Entry      ScopeEntry
	ArchivedAt time.Time
}

// Requirements are what a hacker needs before a program accepts their
// reports. The zero value means anyone can submit.
type Requirements struct {
	MinSignal   *float64 // minimum HackerOne Signal (hackers below may get trial reports)
	IDVerified  bool     // identity-verified hackers only
	Cleared     bool     // background-cleared hackers only
	Application bool     // hackers must apply and be accepted first
	EEA         bool     // residents of the European Economic Area only
	TwoFactor   bool     // the hacker's account must have two-factor auth on
	Terms       bool     // the program's own terms must be accepted first
}

// SeverityStats holds one SeverityStat per severity.
type SeverityStats struct {
	Low      SeverityStat `json:"low"`
	Medium   SeverityStat `json:"medium"`
	High     SeverityStat `json:"high"`
	Critical SeverityStat `json:"critical"`
}

// SeverityStat is the average bounty and number of resolved reports for one
// severity; nil when not published.
type SeverityStat struct {
	AvgBounty *float64 `json:"avg_bounty,omitempty"`
	Reports   *int     `json:"reports,omitempty"`
}

// ProgramProfile is descriptive text from a program's page, besides the
// policy.
type ProgramProfile struct {
	Website      string `json:"website,omitempty"`      // the company's own site
	Introduction string `json:"introduction,omitempty"` // Markdown
	Campaign     string `json:"campaign,omitempty"`     // current campaign or bounty table notes, Markdown
	// ScopeExclusions are extra classes of findings the program won't
	// accept, on top of the platform's standard list.
	ScopeExclusions []Exclusion `json:"scope_exclusions,omitempty"`
	// StandardsExclusions are platform standards the program opted out of;
	// empty means fully compliant.
	StandardsExclusions []Exclusion `json:"standards_exclusions,omitempty"`
	// StandardsKnown is true when the platform reported standards compliance.
	StandardsKnown bool `json:"standards_known,omitempty"`
	// CredentialScopes are the platform IDs of scope entries (see
	// ScopeEntry.PlatformID) with test credentials the platform hands out.
	CredentialScopes []string `json:"credential_scopes,omitempty"`
	// CredentialPool is set when the program hands out test credentials
	// for the program as a whole, on request (Intigriti).
	CredentialPool *CredentialPool `json:"credential_pool,omitempty"`
	// Brief sections besides the policy (Intigriti), Markdown: notes on
	// what's in scope, what's out of scope, how severity is judged, and
	// the program's FAQ.
	InScope  string `json:"in_scope,omitempty"`
	OutScope string `json:"out_of_scope,omitempty"`
	Severity string `json:"severity,omitempty"`
	FAQ      string `json:"faq,omitempty"`
}

// CredentialPool is a program's test accounts, handed out on request.
type CredentialPool struct {
	Available bool `json:"available"` // some are left to hand out now
	Max       int  `json:"max"`       // how many one researcher can get
}

// Exclusion is one named exclusion and its explanation.
type Exclusion struct {
	Name    string `json:"name"`
	Details string `json:"details,omitempty"`
}

// RewardRow is one row of a program's bounty table.
type RewardRow struct {
	Scope    string      `json:"scope"` // asset or group the row applies to
	Low      RewardRange `json:"low"`
	Medium   RewardRange `json:"medium"`
	High     RewardRange `json:"high"`
	Critical RewardRange `json:"critical"`
}

// RewardRange is the bounty for one severity. Min is 0 when the platform
// gives a single amount; Max is 0 when the severity isn't rewarded.
type RewardRange struct {
	Min float64 `json:"min,omitempty"`
	Max float64 `json:"max,omitempty"`
}

// Listing is the result of ListPrograms.
type Listing struct {
	Programs []Program
	Complete bool // every page was fetched; safe to infer removals
	// ChangesSince, if set, means every program whose scope changed after
	// it has Program.ScopeChangedAt set (e.g. from an activity feed), so
	// programs without one are unchanged since then.
	ChangesSince *time.Time
	// ChangesErr is set when the platform's change feed failed. The listing
	// itself is fine; polls just can't skip unchanged scopes.
	ChangesErr error
}

// Program states.
const (
	StateOpen   = "open"
	StatePaused = "paused"
	StateClosed = "closed"
)

// Program is one bug bounty or VDP program as the platform reports it.
type Program struct {
	Handle       string // stable per-platform identifier used in URLs
	PlatformID   string
	Name         string
	URL          string
	Private      bool
	State        string // StateOpen, StatePaused or StateClosed
	OffersBounty bool
	Currency     string
	// BountyMin and BountyMax are the bounty range the listing gives, in
	// Currency; nil where it doesn't say.
	BountyMin, BountyMax *float64

	// BountyUnknown is set where the listing can't say whether the program
	// pays (Bugcrowd: a bug bounty engagement without a reward range);
	// OffersBounty is then false and the stored answer stands, for the
	// stats to settle (Facts.OffersBounty).
	BountyUnknown bool
	// PauseUnknown is set where the listing can't tell a paused program
	// from an open one (Bugcrowd: only the brief says); State is then open
	// and a stored pause stands, for the brief to settle
	// (ScopeDetail.State).
	PauseUnknown bool

	FastPayments bool
	SafeHarbor   bool
	TriageActive bool
	OpenScope    bool
	StartedAt    *time.Time
	JoinedAt     *time.Time // when the user accepted the invitation, if known
	Policy       string     // program rules and guidelines, Markdown
	// More facts some platforms give. Nil or empty means the listing
	// doesn't say, and keeps what was stored.
	Collaboration *bool  // reports can be shared with other hackers
	Industry      string // the company's sector, as the platform names it
	Country       string // where the company is, as the platform names it
	TriageService string // the platform's triage service level (Bugcrowd)
	ReportCost    *int   // credits a report costs to submit (YesWeHack)
	// BountySplitting: a bounty can be split with collaborators (HackerOne).
	BountySplitting *bool
	OffersSwag      *bool // the program gives swag (YesWeHack)
	// Requirements are what the listing says a hacker needs to submit; nil
	// when the listing doesn't say (they may come with the stats instead).
	Requirements *Requirements
	// ScopeChangedAt is when the listing says the program's scope last
	// changed (or the program itself, if the platform doesn't separate them);
	// nil if it doesn't say. Polls skip fetching scope that hasn't changed.
	ScopeChangedAt *time.Time

	UserStats *UserStats // the user's own numbers on this program, if exposed

	// Raw is the platform's JSON for this program, kept for fields we
	// don't model yet.
	Raw json.RawMessage
}

// UserStats are the user's own numbers on one program.
type UserStats struct {
	Reports      int
	ValidReports int
	BountyEarned float64
}

// ScopeEntry is one in-scope or out-of-scope asset exactly as the platform
// lists it.
type ScopeEntry struct {
	PlatformID        string // empty if the platform has no per-entry ID
	InScope           bool
	AssetType         string // normalised; see internal/scope
	PlatformAssetType string
	Identifier        string
	BountyEligible    bool
	MaxSeverity       string
	Instruction       string
	CreatedAt         *time.Time
	UpdatedAt         *time.Time
	// CVSS is how much the asset's confidentiality, integrity and
	// availability matter to the program (HackerOne); empty when not given.
	// Changes to it aren't tracked as scope changes.
	CVSS CVSSRequirements
}

// CVSSRequirements are an asset's CVSS environmental requirements, as the
// platform words them (e.g. "high", "not_defined"); "" where not given.
type CVSSRequirements struct {
	Confidentiality, Integrity, Availability string
}

// Facts are program facts some platforms give outside the listing, with
// the brief or the stats. Nil or "" means the platform doesn't say, and
// keeps what was stored.
type Facts struct {
	Collaboration *bool  // reports can be shared with other hackers
	SafeHarbor    string // the safe harbor offered: "full" or "partial" (Bugcrowd)
	Disclosure    *bool  // coordinated disclosure is allowed (Bugcrowd)
	OffersSwag    *bool  // the program gives swag (HackerOne)
	// OffersBounty settles whether a program pays where its listing can't
	// say (Program.BountyUnknown): Bugcrowd, from a published average
	// payout.
	OffersBounty *bool
	// StartedAt is when the program launched: HackerOne's public launch
	// (launched_at; its listing's started_accepting_at can be months
	// earlier, invite-only), Bugcrowd's engagement start (programs that
	// predate Bugcrowd's engagements show when they moved to them).
	StartedAt *time.Time
}

// ResearcherStatsFetcher is implemented by platforms that expose the
// user's own researcher standing (e.g. HackerOne's Signal, Impact,
// Reputation and Rank across 90 days, past year and all time).
type ResearcherStatsFetcher interface {
	FetchResearcherStats(ctx context.Context) (ResearcherStats, error)
}

// ResearcherWindowStats holds researcher metrics for one time window.
// Any field the platform hasn't calculated yet is nil.
type ResearcherWindowStats struct {
	Signal           *float64 `json:"signal,omitempty"`
	SignalPercentile *int     `json:"signal_percentile,omitempty"`
	Impact           *float64 `json:"impact,omitempty"`
	ImpactPercentile *int     `json:"impact_percentile,omitempty"`
	Reputation       *int     `json:"reputation,omitempty"`
	Rank             *int     `json:"rank,omitempty"`
}

// ResearcherStats is the user's own standing on a platform across time windows.
type ResearcherStats struct {
	Username           string                `json:"username,omitempty"`
	Last90Days         ResearcherWindowStats `json:"last_90_days"`
	PastYear           ResearcherWindowStats `json:"past_year"`
	AllTime            ResearcherWindowStats `json:"all_time"`
	TrialResetOnTriage *bool                 `json:"trial_reset_on_triage,omitempty"`
}
