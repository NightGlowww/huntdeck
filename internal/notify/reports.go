package notify

import (
	"net/url"
	"strconv"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/reports"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Notifications about the user's own reports: syncs keep what they find
// new (store.ReportNotice) and the notifier queues them like a poll's
// changes, one event a notice. They go only to the channels that choose
// these kinds, and only the platform filter applies: a channel's program
// filters (bounty, private, tags) are for programs to hunt, not the
// user's own reports.

// Report is the user's report an event is about. Only its title and
// state: HuntDeck keeps no write-ups or comments.
type Report struct {
	ID            int64    `json:"id"`
	Title         string   `json:"title"`
	URL           string   `json:"url,omitempty"` // on the platform
	State         string   `json:"state"`
	Severity      string   `json:"severity,omitempty"`
	ProgramName   string   `json:"program_name"`
	ProgramHandle string   `json:"program_handle,omitempty"`
	Private       bool     `json:"private"`
	Amount        *float64 `json:"amount,omitempty"` // a bounty's
	Currency      string   `json:"currency,omitempty"`
	HuntDeckURL   string   `json:"huntdeck_url,omitempty"` // set when sent, if the address is known
}

// FromReportNotices turns news on the user's reports into events, one a
// notice.
func FromReportNotices(ns []store.ReportNotice) []Event {
	out := make([]Event, 0, len(ns))
	for _, n := range ns {
		r := n.Report
		if r.ID == 0 { // deleted since
			continue
		}
		name := r.ProgramName
		if name == "" {
			name = r.ProgramHandle
		}
		e := Event{At: n.OccurredAt, Platform: r.Platform, Old: n.From, New: n.To,
			Report: &Report{ID: r.ID, Title: r.Title, URL: r.URL, State: r.State, Severity: r.Severity, ProgramName: name,
				ProgramHandle: r.ProgramHandle, Private: r.ProgramPrivate}}
		switch n.Kind {
		case store.ReportChangeState:
			e.Kind = EventReportState
			e.Detail = reports.Label(n.From) + " → " + reports.Label(n.To)
		case store.ReportChangeSeverity:
			e.Kind = EventReportSeverity
			e.Detail = severityLabel(n.From) + " → " + severityLabel(n.To)
		case store.ReportChangeBounty:
			e.Kind = EventReportBounty
			e.Report.Amount, e.Report.Currency = n.Amount, n.Currency
			what := map[string]string{"bonus": "Bonus", "retest": "Retest"}[n.To]
			if what == "" {
				what = "Bounty"
			}
			if n.Amount != nil {
				e.Detail = what + " of " + humanize.MoneyFull(*n.Amount, n.Currency)
			} else {
				e.Detail = what + " awarded"
			}
		case store.ReportChangeWaiting:
			e.Kind = EventReportWaiting
			e.Detail = "The program is waiting on your reply"
		case store.ReportChangeNew:
			e.Kind = EventReportNew
			e.Detail = "Submitted " + r.SubmittedAt.UTC().Format("2 Jan 2006") + " · " + reports.Label(n.To)
		default:
			continue
		}
		out = append(out, e)
	}
	return out
}

func severityLabel(s string) string {
	for _, sv := range reports.Severities {
		if sv.Key == s {
			return sv.Label
		}
	}
	return "Unrated"
}

// reportURL is where a message links a report: its page in HuntDeck when
// the address is known, else the platform's.
func (r renderer) reportURL(rep *Report) string {
	if r.base != "" {
		return r.base + "/reports/" + url.PathEscape(strconv.FormatInt(rep.ID, 10))
	}
	return rep.URL
}

// reportLine is a report event's second line: the program, then the
// report's title, linked.
func (r renderer) reportLine(rep *Report) string {
	st := r.st
	title := st.esc(truncate(rep.Title, 200))
	if u := r.reportURL(rep); u != "" {
		title = st.link(title, u)
	}
	line := st.bold(st.esc(rep.ProgramName)) + " · " + title
	if rep.Private {
		line = st.bold(st.esc(rep.ProgramName)) + st.esc(" (private)") + " · " + title
	}
	return line
}
