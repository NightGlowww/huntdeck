package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/reports"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Adding a report by hand (/reports/new) and correcting one
// (/reports/{id}/edit). On a synced report only the fields changed are
// kept as set by hand; syncs leave them alone until "Use the platform's
// values" undoes that.

type reportPaymentForm struct {
	Kind, Amount, Currency, Date string
}

type reportForm struct {
	ID                int64
	Source            string // "" for a new report
	Platform          string
	ProgramName       string
	Title             string
	URL               string
	State             string
	Severity          string
	SubmittedSeverity string
	Submitted         string // YYYY-MM-DD
	Closed            string
	Payments          []reportPaymentForm
	Edits             map[string]bool
	Error             string
	Platforms         []string
}

// Manual reports whether the form is for a report added by hand (new or
// edited), whose every field is the user's.
func (f reportForm) Manual() bool { return f.Source == "" || f.Source == store.ReportByHand }

// Heading is the form page's title.
func (f reportForm) Heading() string {
	if f.ID == 0 {
		return "Add a report"
	}
	return "Edit report"
}

// Action is where the form posts.
func (f reportForm) Action() string {
	if f.ID == 0 {
		return "/reports/new"
	}
	return "/reports/" + strconv.FormatInt(f.ID, 10) + "/edit"
}

// States and Severities are the form's choices.
func (reportForm) States() []struct{ Key, Label string }     { return reportStateOptions() }
func (reportForm) Severities() []struct{ Key, Label string } { return reportSeverityOptions() }

// reportFormPlatforms are the platforms a report can be added for: the
// four, programs added by hand, and anywhere else.
var reportFormPlatforms = []string{"hackerone", "bugcrowd", "intigriti", "yeswehack", "independent", "other"}

const paymentRows = 3

func (s *server) newReportForm(w http.ResponseWriter, r *http.Request) {
	f := reportForm{Platform: "independent", State: "new", Submitted: s.Store.Now().In(s.shownZone()).Format(time.DateOnly)}
	s.renderReportForm(w, r, http.StatusOK, f)
}

func (s *server) editReportForm(w http.ResponseWriter, r *http.Request) {
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
	s.renderReportForm(w, r, http.StatusOK, s.formOf(rep))
}

func (s *server) formOf(rep store.MyReport) reportForm {
	zone := s.shownZone()
	f := reportForm{ID: rep.ID, Source: rep.Source, Platform: rep.Platform, ProgramName: rep.ProgramName, Title: rep.Title, URL: rep.URL,
		State: rep.State, Severity: rep.Severity, SubmittedSeverity: rep.SubmittedSeverity,
		Submitted: rep.SubmittedAt.In(zone).Format(time.DateOnly), Edits: rep.Edits}
	if rep.ClosedAt != nil {
		f.Closed = rep.ClosedAt.In(zone).Format(time.DateOnly)
	}
	for _, p := range rep.Payments {
		f.Payments = append(f.Payments, reportPaymentForm{Kind: p.Kind, Amount: strconv.FormatFloat(p.Amount, 'f', -1, 64),
			Currency: p.Currency, Date: p.AwardedAt.In(zone).Format(time.DateOnly)})
	}
	return f
}

func (s *server) renderReportForm(w http.ResponseWriter, r *http.Request, status int, f reportForm) {
	f.Platforms = reportFormPlatforms
	for len(f.Payments) < paymentRows && f.Manual() {
		f.Payments = append(f.Payments, reportPaymentForm{Kind: "bounty"})
	}
	s.render(w, r, status, "reportform", pageData{Title: f.Heading(), Nav: "reports", Data: f, FlashErr: f.Error})
}

// readReportForm reads the form into f and the store's input.
func (s *server) readReportForm(form url.Values, f *reportForm) (store.ReportInput, string) {
	zone := s.shownZone()
	trim := func(k string) string { return strings.TrimSpace(form.Get(k)) }
	f.ProgramName, f.Title, f.URL = trim("program"), trim("title"), trim("url")
	f.State, f.Severity, f.SubmittedSeverity, f.Submitted, f.Closed = trim("state"), trim("severity"), trim("submitted_severity"), trim("submitted"), trim("closed")
	if f.Manual() {
		f.Platform = trim("platform")
	}
	in := store.ReportInput{Platform: f.Platform, ProgramName: f.ProgramName, Title: f.Title, URL: f.URL, State: f.State,
		Severity: f.Severity, SubmittedSeverity: f.SubmittedSeverity}
	switch {
	case f.Title == "":
		return in, "Give the report a title."
	case len(f.Title) > 500 || len(f.ProgramName) > 200 || len(f.URL) > 2000:
		return in, "That's too long."
	case f.URL != "" && httpURL(f.URL) == "":
		return in, "The link must start with https:// or http://."
	case !validState(f.State):
		return in, "Choose a state."
	case !validSeverity(f.Severity) || !validSeverity(f.SubmittedSeverity):
		return in, "Choose a severity."
	case f.Manual() && !validPlatform(f.Platform):
		return in, "Choose a platform."
	}
	t, err := time.ParseInLocation(time.DateOnly, f.Submitted, zone)
	if err != nil {
		return in, "The submission date should be a date: 2026-03-14."
	}
	in.SubmittedAt = t.UTC()
	if f.Closed != "" {
		c, err := time.ParseInLocation(time.DateOnly, f.Closed, zone)
		if err != nil || c.Before(t) {
			return in, "The closing date should be a date on or after the submission."
		}
		cu := c.UTC()
		in.ClosedAt = &cu
	}
	if !f.Manual() {
		return in, ""
	}
	f.Payments = nil
	for i := range paymentRows {
		p := reportPaymentForm{Kind: trim(fmt.Sprintf("pay_kind_%d", i)), Amount: trim(fmt.Sprintf("pay_amount_%d", i)),
			Currency: strings.ToUpper(trim(fmt.Sprintf("pay_currency_%d", i))), Date: trim(fmt.Sprintf("pay_date_%d", i))}
		f.Payments = append(f.Payments, p)
		if p.Amount == "" {
			continue
		}
		amount, err := strconv.ParseFloat(strings.ReplaceAll(p.Amount, ",", ""), 64)
		if err != nil || amount <= 0 || amount > 1e9 {
			return in, "A payment's amount should be a number, such as 1500."
		}
		if len(p.Currency) != 3 || strings.Trim(p.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
			return in, "A payment's currency should be a three-letter code, such as USD."
		}
		at, err := time.ParseInLocation(time.DateOnly, p.Date, zone)
		if err != nil {
			return in, "A payment needs the date it was awarded."
		}
		if p.Kind != "bounty" && p.Kind != "bonus" && p.Kind != "retest" {
			p.Kind = "other"
		}
		in.Payments = append(in.Payments, store.MyPayment{Kind: p.Kind, Amount: amount, Currency: p.Currency, AwardedAt: at.UTC()})
	}
	return in, ""
}

func validState(s string) bool {
	for _, st := range reports.States {
		if st.Key == s {
			return true
		}
	}
	return false
}

func validSeverity(s string) bool {
	if s == "" {
		return true
	}
	for _, sv := range reports.Severities {
		if sv.Key == s {
			return true
		}
	}
	return false
}

func validPlatform(p string) bool {
	for _, v := range reportFormPlatforms {
		if v == p {
			return true
		}
	}
	return false
}

func (s *server) saveNewReport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	var f reportForm
	in, problem := s.readReportForm(r.PostForm, &f)
	if problem != "" {
		f.Error = problem
		s.renderReportForm(w, r, http.StatusBadRequest, f)
		return
	}
	id, err := s.Store.AddManualReport(r.Context(), in)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/reports/"+strconv.FormatInt(id, 10)+"?msg=report-saved", http.StatusSeeOther)
}

func (s *server) saveReportEdit(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "No such report.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
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
	before := s.formOf(rep)
	f := before
	in, problem := s.readReportForm(r.PostForm, &f)
	if problem != "" {
		f.Error = problem
		s.renderReportForm(w, r, http.StatusBadRequest, f)
		return
	}
	// Only what changed counts as set by hand.
	for field, changed := range map[string]bool{"title": f.Title != before.Title, "program_name": f.ProgramName != before.ProgramName,
		"url": f.URL != before.URL, "state": f.State != before.State, "severity": f.Severity != before.Severity,
		"submitted_severity": f.SubmittedSeverity != before.SubmittedSeverity, "submitted_at": f.Submitted != before.Submitted} {
		if changed || (rep.Source == store.ReportByHand) {
			in.Set = append(in.Set, field)
		}
	}
	if rep.Source == store.ReportByHand {
		in.Set = append(in.Set, "payments")
	}
	if err := s.Store.UpdateReport(r.Context(), id, in); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/reports/"+strconv.FormatInt(id, 10)+"?msg=report-saved", http.StatusSeeOther)
}
