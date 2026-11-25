package web

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/reports"
	"github.com/mdpsec/huntdeck/internal/store"
)

// The Reports list as a spreadsheet (/reports.csv): every report the
// filters keep, in the list's order, with the columns it shows (?cols=,
// from the column picker; all when not given). Private like the page: a
// read-only copy doesn't serve it.

// reportCSVLead are the columns every row starts with.
// The program first, as on every list.
var reportCSVLead = []string{"platform", "program", "program_handle", "report_id", "title", "url"}

// reportCSVColumns are each list column's CSV fields, by its key.
var reportCSVColumns = []struct {
	key    string
	header []string
	values func(store.MyReport, *time.Location) []string
}{
	{"sev", []string{"severity", "submitted_severity"}, func(r store.MyReport, _ *time.Location) []string {
		return []string{r.Severity, r.SubmittedSeverity}
	}},
	{"state", []string{"state", "platform_state"}, func(r store.MyReport, _ *time.Location) []string {
		return []string{r.State, r.RawState}
	}},
	{"bounty", []string{"earned"}, func(r store.MyReport, _ *time.Location) []string {
		var parts []string
		for _, a := range reports.ReportEarned(r) {
			parts = append(parts, strconv.FormatFloat(a.Value, 'f', -1, 64)+" "+a.Currency)
		}
		return []string{strings.Join(parts, "; ")}
	}},
	{"submitted", []string{"submitted"}, func(r store.MyReport, z *time.Location) []string {
		return []string{csvDate(&r.SubmittedAt, z)}
	}},
	{"activity", []string{"last_activity", "comments"}, func(r store.MyReport, z *time.Location) []string {
		return []string{csvDate(r.LastActivityAt, z), strconv.Itoa(r.Comments)}
	}},
	{"triaged", []string{"triaged"}, func(r store.MyReport, z *time.Location) []string {
		return []string{csvDate(r.TriagedAt, z)}
	}},
	{"closed", []string{"closed"}, func(r store.MyReport, z *time.Location) []string {
		return []string{csvDate(r.ClosedAt, z)}
	}},
}

func csvDate(t *time.Time, zone *time.Location) string {
	if t == nil {
		return ""
	}
	return t.In(zone).Format(time.DateOnly)
}

func (s *server) myReportsCSV(w http.ResponseWriter, r *http.Request) {
	all, err := s.Store.MyReports(r.Context(), store.ReportFilter{})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	q := r.URL.Query()
	zone := s.shownZone()
	_, quiet := s.quietAfter(r.Context())
	f := reportQueryFrom(q, "open", zone, s.Store.Now(), quiet)
	var shown []store.MyReport
	for _, rep := range all {
		if f.match(rep, "") {
			shown = append(shown, rep)
		}
	}
	sortReports(shown, q.Get("sort"), q.Get("rev") == "1")
	want := map[string]bool{}
	for _, k := range strings.Split(q.Get("cols"), ",") {
		want[strings.TrimSpace(k)] = true
	}
	cols := reportCSVColumns[:0:0]
	for _, c := range reportCSVColumns {
		if q.Get("cols") == "" || want[c.key] {
			cols = append(cols, c)
		}
	}
	header := append([]string{}, reportCSVLead...)
	for _, c := range cols {
		header = append(header, c.header...)
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="huntdeck-reports.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write(header)
	for _, rep := range shown {
		row := []string{rep.Platform, csvText(rep.ProgramName), csvText(rep.ProgramHandle), rep.Key, csvText(rep.Title), csvText(rep.URL)}
		for _, c := range cols {
			for _, v := range c.values(rep, zone) {
				row = append(row, csvText(v))
			}
		}
		if err := cw.Write(row); err != nil {
			return // the client went away
		}
	}
	cw.Flush()
}
