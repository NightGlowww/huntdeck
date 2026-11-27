// Package notify sends notifications about changes to Discord, Telegram or
// any webhook, the channels the user sets up in Settings.
//
// After each poll the notifier turns the poll's recorded changes into
// events, one per program and kind (300 new assets on one program are one
// event), and queues them for every channel whose rules match: as a message
// now, for the channel's daily digest, or both. Messages wait in an outbox
// in the database and are retried with backoff, so a restart or a channel
// outage neither loses nor repeats one. Ignored programs never notify.
//
// Outbound requests go only to the channels the user configured.
package notify

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Event kinds.
const (
	EventInvite            = "invite"
	EventLaunch            = "launch"
	EventScopeAdded        = "scope_added"
	EventScopeRemoved      = "scope_removed"
	EventScopeOut          = "scope_out"     // the out-of-scope list changed
	EventScopeChanged      = "scope_changed" // an entry was edited
	EventReopened          = "reopened"
	EventPaused            = "paused"
	EventRemoved           = "removed"
	EventBountyRaised      = "bounty_raised"
	EventRewardsChanged    = "rewards_changed"
	EventPolicyChanged     = "policy_changed"
	EventAnnouncement      = "announcement"
	EventScanHandoff       = "scan_handoff" // targets were handed to the scanner
	EventScanBlocked       = "scan_blocked" // the second opinion or a check stopped a batch
	EventPlatformProblem   = "platform_problem"
	EventPlatformRecovered = "platform_recovered"
	EventTest              = "test"
	// The user's own reports (reports.go).
	EventReportState    = "report_state"
	EventReportBounty   = "report_bounty"
	EventReportWaiting  = "report_waiting"
	EventReportSeverity = "report_severity"
	EventReportNew      = "report_new"
	// EventReportQuiet is derived, not a change: a report still undecided after
	// the days Settings › Reports sets without a word from the program.
	EventReportQuiet = "report_quiet"
)

// EventKind describes an event kind for Settings.
type EventKind struct {
	Key     string
	Label   string
	Short   string // for lists of kinds: "scope added, reopened"
	Hint    string
	Group   string // Opportunities, Watching or HuntDeck
	Default bool   // on for new channels
}

// Event kind groups, in the channel form's order.
const (
	GroupOpportunities = "Opportunities" // somewhere new to hunt
	GroupWatching      = "Watching"      // programs changing in other ways
	GroupReports       = "Your reports"  // the user's own reports
	GroupHuntDeck      = "HuntDeck"      // HuntDeck itself
)

// EventKinds are the kinds a channel can choose, in display order, group
// by group.
var EventKinds = []EventKind{
	{EventInvite, "New private invite", "invites", "You were invited to a private program", GroupOpportunities, true},
	{EventLaunch, "New program", "new programs", "A public program launched", GroupOpportunities, true},
	{EventScopeAdded, "Scope added", "scope added", "New in-scope assets", GroupOpportunities, true},
	{EventReopened, "Program reopened", "reopened", "Back open after being paused or closed", GroupOpportunities, true},
	{EventBountyRaised, "Bounties raised", "bounties raised", "A higher top bounty, or a program that starts paying", GroupOpportunities, true},
	{EventAnnouncement, "Program announcements", "announcements", "A program posted news for researchers, such as a bonus or new targets (Bugcrowd)", GroupOpportunities, false},
	{EventScanHandoff, "Scan handoff", "scan handoffs", "Targets were handed to the scanner", GroupOpportunities, false},
	{EventScanBlocked, "Scan blocked", "scan blocks", "A check or the decision model stopped a scan batch", GroupOpportunities, false},
	{EventScopeRemoved, "Scope removed", "scope removed", "In-scope assets taken out", GroupWatching, false},
	{EventScopeOut, "Out-of-scope changes", "out-of-scope changes", "Assets added to or removed from the out-of-scope list", GroupWatching, false},
	{EventScopeChanged, "Scope edited", "scope edits", "An asset's bounty eligibility or scope side changed", GroupWatching, false},
	{EventPaused, "Program paused or closed", "pauses", "Programs that pause and reopen again and again are sent once a pause has lasted a day", GroupWatching, false},
	{EventRemoved, "Program no longer listed", "unlisted", "Gone from the platform's listing", GroupWatching, false},
	{EventRewardsChanged, "Other bounty table changes", "other bounty changes", "Lower or reshuffled amounts", GroupWatching, false},
	{EventPolicyChanged, "Rules edited", "rules edits", "Policy, reward notes or exclusions changed", GroupWatching, false},
	{EventReportBounty, "Bounty awarded", "bounties awarded", "A bounty, bonus or retest payment on one of your reports", GroupReports, true},
	{EventReportState, "Report state changed", "report states", "One of your reports was triaged, resolved, closed as a duplicate…", GroupReports, true},
	{EventReportWaiting, "Waiting on you", "waiting on you", "A program asked you something on one of your reports", GroupReports, true},
	{EventReportSeverity, "Report severity changed", "report severities", "Triage rated one of your reports higher or lower", GroupReports, false},
	{EventReportNew, "New report found", "new reports", "A report you submitted, once HuntDeck has read it", GroupReports, false},
	{EventReportQuiet, "Report gone quiet", "reports gone quiet", "A report still undecided after the days Settings › Reports sets with no word from the program", GroupReports, true},
	{EventPlatformProblem, "Platform problems", "platform problems", "Polls of a platform failed three times in a row (login broken, say), and when they work again", GroupHuntDeck, true},
}

// maxAssets caps the assets kept per event; AssetsTotal says how many
// there were.
const maxAssets = 200

// Event is one thing to tell the user about. It's also the webhook
// payload's event object, so its JSON is a public format: add fields,
// don't rename them.
type Event struct {
	Kind        string    `json:"kind"`
	At          time.Time `json:"at"`
	Platform    string    `json:"platform"`
	Program     *Program  `json:"program,omitempty"`
	Assets      []Asset   `json:"assets,omitempty"`
	AssetsTotal int       `json:"assets_total,omitempty"` // all of them, when Assets is cut short
	Old         string    `json:"old,omitempty"`          // state, top bounty…, before
	New         string    `json:"new,omitempty"`          // and after
	Detail      string    `json:"detail,omitempty"`       // a sentence about it
	ChangeID    int64     `json:"change_id,omitempty"`    // the change with a diff page
	Report      *Report   `json:"report,omitempty"`       // the user's own report it's about
}

// Program is the program an event is about.
type Program struct {
	Platform    string   `json:"platform"`
	Handle      string   `json:"handle"`
	Name        string   `json:"name"`
	Private     bool     `json:"private"`
	Bounty      bool     `json:"bounty"`
	TopBounty   *float64 `json:"top_bounty,omitempty"`
	Currency    string   `json:"currency,omitempty"`
	HuntScore   *float64 `json:"hunt_score,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	URL         string   `json:"url"`                    // on the platform
	HuntDeckURL string   `json:"huntdeck_url,omitempty"` // set when sent, if the address is known
	ignored     bool
}

// Asset is a scope entry an event is about.
type Asset struct {
	Identifier  string `json:"identifier"`
	Type        string `json:"type"`
	InScope     bool   `json:"in_scope"`
	Bounty      bool   `json:"bounty"`
	MaxSeverity string `json:"max_severity,omitempty"`
	NewRoot     bool   `json:"new_root,omitempty"` // a root domain new to the program
}

// count is how many assets an event is about.
func (e Event) count() int {
	return max(e.AssetsTotal, len(e.Assets))
}

// LongPause is how long a flip-flopping program (see store.FlipFlops) must
// stay paused before it's notified; its reopening is notified only after
// one.
const LongPause = 24 * time.Hour

// flipping reports whether state change c is a flip-flopping program's,
// which says little: a pause (until it has lasted LongPause; see
// store.LongPauses), or a reopening after a short pause.
func flipping(c store.NotifyChange) bool {
	if c.LongPause || c.Flips < store.FlipFlops {
		return false
	}
	if c.NewValue != platform.StateOpen {
		return true
	}
	return c.PrevStateAt == nil || c.OccurredAt.Sub(*c.PrevStateAt) < LongPause
}

// FromChanges turns a poll's changes into events, one per program and
// kind, in the order they first happened.
func FromChanges(changes []store.NotifyChange) []Event {
	var out []Event
	index := map[string]int{}
	add := func(c store.NotifyChange, kind string, fill func(*Event)) {
		key := kind + "\x00" + strconv.FormatInt(c.Program.ID, 10)
		i, ok := index[key]
		if !ok {
			i = len(out)
			index[key] = i
			out = append(out, Event{Kind: kind, At: c.OccurredAt, Platform: c.Program.Platform, Program: programOf(c.Program)})
		}
		fill(&out[i])
	}
	addAsset := func(e *Event, c store.NotifyChange) {
		if c.Entry == nil {
			return
		}
		e.AssetsTotal = e.count() + 1
		if len(e.Assets) < maxAssets {
			en := c.Entry
			e.Assets = append(e.Assets, Asset{Identifier: assetName(en.Identifier), Type: en.AssetType, InScope: en.InScope,
				Bounty: en.Bounty && c.Program.OffersBounty, MaxSeverity: en.MaxSeverity, NewRoot: en.NewRoot && en.InScope})
		}
	}
	for _, c := range changes {
		switch c.Kind {
		case store.ChangeProgramAdded:
			kind := EventLaunch
			if c.NewValue == "private" {
				kind = EventInvite
			}
			add(c, kind, func(*Event) {})
		case store.ChangeProgramRemoved:
			add(c, EventRemoved, func(*Event) {})
		case store.ChangeStateChanged:
			if flipping(c) {
				continue
			}
			kind := EventPaused
			if c.NewValue == platform.StateOpen {
				kind = EventReopened
			}
			add(c, kind, func(e *Event) {
				e.Old, e.New = c.OldValue, c.NewValue
				if c.LongPause {
					e.Detail = "Paused for over a day, after " + strconv.Itoa(c.Flips-1) + " status changes in the week before"
				}
			})
		case store.ChangeBountyChanged:
			if c.NewValue == "true" {
				add(c, EventBountyRaised, func(e *Event) { e.Detail = "Now pays bounties" })
			} else {
				add(c, EventRewardsChanged, func(e *Event) { e.Detail = "No longer pays bounties" })
			}
		case store.ChangeRewardsChanged:
			oldTop, newTop := tableTop(c.OldValue), tableTop(c.NewValue)
			kind := EventRewardsChanged
			if newTop > oldTop {
				kind = EventBountyRaised
			}
			add(c, kind, func(e *Event) {
				e.ChangeID = c.ID
				if oldTop != newTop {
					e.Old, e.New = humanize.MoneyFull(oldTop, c.Program.Currency), humanize.MoneyFull(newTop, c.Program.Currency)
					e.Detail = "Top bounty " + e.Old + " → " + e.New
				} else {
					e.Detail = "Bounty table changed"
				}
			})
		case store.ChangeAnnouncement:
			add(c, EventAnnouncement, func(e *Event) {
				e.ChangeID = c.ID
				e.Detail = joinDetail(e.Detail, announcementTitle(c.NewValue))
			})
		case store.ChangePolicyChanged:
			add(c, EventPolicyChanged, func(e *Event) {
				e.ChangeID = c.ID
				e.Detail = joinDetail(e.Detail, policyLabels[c.Field])
			})
		case store.ChangeScopeAdded:
			kind := EventScopeAdded
			if c.Entry != nil && !c.Entry.InScope {
				kind = EventScopeOut
			}
			add(c, kind, func(e *Event) { addAsset(e, c) })
		case store.ChangeScopeRemoved:
			kind := EventScopeRemoved
			if c.Field == "out_of_scope" {
				kind = EventScopeOut
			}
			add(c, kind, func(e *Event) { addAsset(e, c) })
		case store.ChangeScopeUpdated:
			if c.Field == "in_scope" || c.Field == "bounty_eligible" {
				add(c, EventScopeChanged, func(e *Event) { addAsset(e, c) })
			}
		}
	}
	return out
}

var policyLabels = map[string]string{store.FieldPolicy: "policy", store.FieldCampaign: "reward notes", store.FieldExclusions: "exclusions",
	store.FieldInScope: "in-scope notes", store.FieldOutScope: "out of scope", store.FieldSeverity: "severity assessment", store.FieldFAQ: "FAQ"}

func joinDetail(have, add string) string {
	if add == "" || strings.Contains(have, add) {
		return have
	}
	if have == "" {
		return "Edited: " + add
	}
	return have + ", " + add
}

func programOf(p store.NotifyProgram) *Program {
	return &Program{Platform: p.Platform, Handle: p.Handle, Name: p.Name, Private: p.Private, Bounty: p.OffersBounty,
		TopBounty: p.TopBounty, Currency: p.Currency, HuntScore: p.HuntScore, Tags: p.Tags, URL: p.URL, ignored: p.Ignored}
}

func tableTop(raw string) float64 {
	var rows []platform.RewardRow
	_ = json.Unmarshal([]byte(raw), &rows) // a bad table reads as empty
	top := 0.0
	for _, r := range rows {
		top = max(top, r.Low.Max, r.Medium.Max, r.High.Max, r.Critical.Max)
	}
	return top
}

// merge combines events of the same program and kind (a day's polls, for
// a digest), keeping the latest program details.
func merge(events []Event) []Event {
	var out []Event
	index := map[string]int{}
	for _, e := range events {
		key := e.Kind + "\x00" + e.Platform
		if e.Program != nil {
			key += "\x00" + e.Program.Handle
		}
		i, ok := index[key]
		if !ok || e.Kind == EventPlatformProblem || e.Kind == EventPlatformRecovered {
			index[key] = len(out)
			out = append(out, e)
			continue
		}
		m := &out[i]
		total := m.count() + e.count()
		for _, a := range e.Assets {
			if len(m.Assets) < maxAssets {
				m.Assets = append(m.Assets, a)
			}
		}
		if total > 0 {
			m.AssetsTotal = total
		}
		m.At, m.Program, m.New = e.At, e.Program, e.New
		if e.ChangeID != 0 {
			m.ChangeID = e.ChangeID
		}
		if e.Detail != "" {
			m.Detail = e.Detail
		}
	}
	return out
}

func platformLabel(name string) string {
	switch name {
	case "hackerone":
		return "HackerOne"
	case "bugcrowd":
		return "Bugcrowd"
	case "intigriti":
		return "Intigriti"
	case "yeswehack":
		return "YesWeHack"
	case "vulbox":
		return "VulBox"
	}
	return name
}

// announcementTitle is an announcement change's title: its first line.
func announcementTitle(v string) string {
	title, _, _ := strings.Cut(v, "\n")
	return strings.TrimSpace(title)
}

// assetName is an asset's identifier, or what it is where the platform
// hid its name.
func assetName(identifier string) string {
	if scope.HiddenName(identifier) {
		return "(" + scope.HiddenLabel + ")"
	}
	return identifier
}
