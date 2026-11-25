package platform

import (
	"context"
	"encoding/json"
	"time"
)

// Normalised report states (Report.State). Each platform maps its own onto
// these; the platform's words are kept in Report.RawState.
const (
	ReportNew           = "new"            // nobody has decided yet
	ReportTriaged       = "triaged"        // the platform's triage passed it to the program
	ReportAccepted      = "accepted"       // valid, not fixed yet
	ReportResolved      = "resolved"       // valid and fixed
	ReportAcceptedRisk  = "accepted_risk"  // valid, the program won't fix it
	ReportDuplicate     = "duplicate"      // someone reported it first
	ReportInformative   = "informative"    // true but no security impact
	ReportNotApplicable = "not_applicable" // not a vulnerability, or not reproduced
	ReportOutOfScope    = "out_of_scope"
	ReportSpam          = "spam"
)

// Severities (Report.Severity and SubmittedSeverity): "" is unrated.
const (
	SeverityNone     = "none"
	SeverityLow      = "low"
	SeverityMedium   = "medium"
	SeverityHigh     = "high"
	SeverityCritical = "critical"
)

// Report is one of the account's own reports, as a platform shows it to its
// author. Never the write-up and never comment text: comments are counted.
type Report struct {
	Key           string // the platform's ID for it
	ProgramHandle string // as HuntDeck stores the program's handle, when it can be told
	ProgramName   string
	Title         string
	URL           string

	State    string // a Report* constant
	RawState string // the platform's own state

	Severity      string // a Severity* constant, or ""
	SeverityLabel string // the platform's word when it says more (P2, Exceptional)
	// SubmittedSeverity is what the report was submitted as ("" when the
	// platform doesn't say). SeverityChanged is true when triage changed
	// it, which a platform may say without saying from what.
	SubmittedSeverity string
	SeverityChanged   bool
	CVSS              *float64

	SubmittedAt           time.Time
	FirstResponseAt       *time.Time
	TriagedAt             *time.Time
	FirstBountyAt         *time.Time
	ClosedAt              *time.Time
	LastActivityAt        *time.Time
	LastProgramActivityAt *time.Time
	LastHunterActivityAt  *time.Time

	WaitingOn   string // "program", "you" or ""
	Comments    int
	Unread      bool
	DuplicateOf string

	// Marker changes whenever anything on the report does; details are
	// read again only when it moved since the last read.
	Marker string
	// Detailed means the details were read this time: Events (and, unless
	// PaymentsKnown says so from elsewhere, Payments) are the whole set.
	Detailed bool
	// PaymentsKnown means Payments is the report's whole set, from its
	// details or from an account-wide earnings list.
	PaymentsKnown bool
	Payments      []ReportPayment
	Events        []ReportEvent

	// Raw is an allow-list of the platform's fields, never a write-up.
	Raw json.RawMessage
}

// ReportPayment is money a report earned, in the currency awarded.
type ReportPayment struct {
	Ref       string // the platform's ID for it, when it has one
	Kind      string // bounty, bonus, retest, other
	Amount    float64
	Currency  string
	AwardedAt time.Time
	PaidAt    *time.Time
	Status    string
}

// ReportEvent is one step of a report's timeline. No text.
type ReportEvent struct {
	At   time.Time
	Kind string // state, severity, bounty, comment_program, comment_you
	From string
	To   string
}

// Report event kinds.
const (
	EventState          = "state"
	EventSeverity       = "severity"
	EventBounty         = "bounty"
	EventCommentProgram = "comment_program"
	EventCommentYou     = "comment_you"
)

// ReportsSaver stores a batch of reports as they're read.
type ReportsSaver func(ctx context.Context, reports []Report) error

// ReportFetcher is implemented by platforms that can list the account's own
// reports.
type ReportFetcher interface {
	// ReportsConfigured reports whether the login reports need is set up.
	ReportsConfigured() bool
	// FetchReports lists the account's reports and reads the details of
	// those whose Marker isn't known[Key] (new, or changed since), newest
	// first, at most maxDetails of them; the rest are saved from the
	// listing alone (Detailed false) and read on a later run. It calls save
	// with batches as it goes. complete is true only when every page of the
	// listing was read, so a report missing from it is really gone.
	FetchReports(ctx context.Context, known map[string]string, maxDetails int, save ReportsSaver) (complete bool, err error)
}
