package web

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/reports"
	"github.com/mdpsec/huntdeck/internal/store"
)

// One of the user's reports (/reports/{id}): its timeline, payments and
// times, with snooze, mute and edit. Private, like the list.

type reportStep struct {
	Label string
	At    *time.Time
	After string // time since submission
}

type reportEventView struct {
	At    time.Time
	Label string
	Class string
}

type reportView struct {
	reportRow
	Steps  []reportStep
	Events []reportEventView
	// Snoozed is the date the report sleeps until, when it does.
	Snoozed *time.Time
}

func reportID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func (s *server) myReport(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "No such report.")
		return
	}
	rep, err := s.Store.MyReportByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "No such report.")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	events, err := s.Store.MyReportEvents(r.Context(), id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	now := s.Store.Now()
	_, quiet := s.quietAfter(r.Context())
	v := reportView{reportRow: s.reportRow(rep, now, quiet)}
	if rep.SnoozedUntil != nil && rep.SnoozedUntil.After(now) {
		v.Snoozed = rep.SnoozedUntil
	}
	after := func(t *time.Time) string {
		if t == nil || t.Before(rep.SubmittedAt) {
			return ""
		}
		return humanDuration(t.Sub(rep.SubmittedAt))
	}
	submitted := rep.SubmittedAt
	v.Steps = []reportStep{
		{Label: "Submitted", At: &submitted},
		{Label: "First response", At: rep.FirstResponseAt, After: after(rep.FirstResponseAt)},
		{Label: "Triaged", At: rep.TriagedAt, After: after(rep.TriagedAt)},
		{Label: "First bounty", At: rep.FirstBountyAt, After: after(rep.FirstBountyAt)},
		{Label: "Closed", At: rep.ClosedAt, After: after(rep.ClosedAt)},
	}
	v.Events = reportTimeline(events, rep.Payments)
	s.render(w, r, http.StatusOK, "report", pageData{Title: rep.Title, Nav: "reports", Data: v})
}

// reportTimeline is the report's steps with its payments among them: a
// bounty the platform's timeline already has gets its amount and currency
// (the same amount, or else the same day), and the rest (retests, bonuses,
// bounties the timeline doesn't show) are steps of their own.
func reportTimeline(events []store.MyReportEvent, pays []store.MyPayment) []reportEventView {
	out := make([]reportEventView, 0, len(events)+len(pays))
	var bounties []int // out's bounty steps not matched to a payment yet
	for _, e := range events {
		if e.Kind == platform.EventBounty {
			bounties = append(bounties, len(out))
		}
		out = append(out, eventView(e))
	}
	amountOf := func(i int) (float64, bool) {
		f, err := strconv.ParseFloat(strings.TrimSpace(events[i].To), 64)
		return f, err == nil
	}
	for _, p := range pays {
		label := paymentLabel(p)
		if p.Kind == "bounty" {
			match := slices.IndexFunc(bounties, func(i int) bool { a, ok := amountOf(i); return ok && a == p.Amount })
			if match < 0 {
				match = slices.IndexFunc(bounties, func(i int) bool {
					return out[i].At.UTC().Format(time.DateOnly) == p.AwardedAt.UTC().Format(time.DateOnly)
				})
			}
			if match >= 0 {
				out[bounties[match]].Label = label
				bounties = slices.Delete(bounties, match, match+1)
				continue
			}
		}
		out = append(out, reportEventView{At: p.AwardedAt, Label: label, Class: "bounty"})
	}
	slices.SortStableFunc(out, func(a, b reportEventView) int { return a.At.Compare(b.At) })
	return out
}

// paymentLabel says what a payment was: "Retest paid: $150".
func paymentLabel(p store.MyPayment) string {
	amount := humanize.MoneyFull(p.Amount, p.Currency)
	switch p.Kind {
	case "bounty":
		return "Bounty awarded: " + amount
	case "bonus":
		return "Bonus awarded: " + amount
	case "retest":
		return "Retest paid: " + amount
	}
	return "Paid: " + amount
}

func eventView(e store.MyReportEvent) reportEventView {
	ev := reportEventView{At: e.At}
	switch e.Kind {
	case platform.EventState:
		ev.Label, ev.Class = "State: "+e.To, "state"
		if e.From != "" {
			ev.Label = "State: " + e.From + " → " + e.To
		}
	case platform.EventSeverity:
		ev.Label, ev.Class = "Severity: "+orUnrated(e.To), "severity"
		if e.From != "" {
			ev.Label = "Severity: " + e.From + " → " + orUnrated(e.To)
		}
	case platform.EventBounty:
		ev.Label, ev.Class = "Bounty awarded", "bounty"
		if e.To != "" && e.To != "bounty" {
			ev.Label = "Awarded: " + e.To
		}
	case platform.EventCommentProgram:
		ev.Label, ev.Class = "The program commented", "program"
	case platform.EventCommentYou:
		ev.Label, ev.Class = "You commented", "you"
	default:
		ev.Label = e.Kind
	}
	return ev
}

func orUnrated(s string) string {
	if s == "" {
		return "unrated"
	}
	return s
}

// reportAction runs a POST on one report and goes back to it.
func (s *server) reportAction(w http.ResponseWriter, r *http.Request, do func(id int64) (string, error)) {
	id, ok := reportID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "No such report.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	to, err := do(id)
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "No such report.")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if to == "" {
		to = "/reports/" + strconv.FormatInt(id, 10)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// snoozeReport puts a report out of "needs you" and "gone quiet" for a
// week or a month (days=7, 30), or wakes it (days=0).
func (s *server) snoozeReport(w http.ResponseWriter, r *http.Request) {
	s.reportAction(w, r, func(id int64) (string, error) {
		days, _ := strconv.Atoi(r.PostFormValue("days"))
		var until *time.Time
		if days > 0 {
			t := s.Store.Now().Add(time.Duration(min(days, 365)) * 24 * time.Hour)
			until = &t
		}
		return "", s.Store.SnoozeReport(r.Context(), id, until)
	})
}

func (s *server) muteReport(w http.ResponseWriter, r *http.Request) {
	s.reportAction(w, r, func(id int64) (string, error) {
		return "", s.Store.MuteReport(r.Context(), id, r.PostFormValue("muted") == "1")
	})
}

func (s *server) resetReport(w http.ResponseWriter, r *http.Request) {
	s.reportAction(w, r, func(id int64) (string, error) {
		return "/reports/" + strconv.FormatInt(id, 10) + "?msg=report-reset", s.Store.ResetReportEdits(r.Context(), id)
	})
}

func (s *server) deleteReport(w http.ResponseWriter, r *http.Request) {
	s.reportAction(w, r, func(id int64) (string, error) {
		return "/reports?msg=report-deleted", s.Store.DeleteManualReport(r.Context(), id)
	})
}

// reportStateOptions and reportSeverityOptions fill the form's choices.
func reportStateOptions() []struct{ Key, Label string } { return reports.States }

func reportSeverityOptions() []struct{ Key, Label string } { return reports.Severities }
