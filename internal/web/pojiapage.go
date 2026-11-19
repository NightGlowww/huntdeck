package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/pojia"
	"github.com/mdpsec/huntdeck/internal/store"
)

// pojiaPage is the POJIA library's front page: search disclosed reports, look
// up a CVE, browse a class's payloads. Every answer comes from POJIA, cached:
// searches six hours, a CVE a day, the rest a week.
func (s *server) pojiaPage(w http.ResponseWriter, r *http.Request) {
	if s.PojiaKey.Reveal() == "" {
		s.render(w, r, http.StatusOK, "pojia", pageData{Title: "POJIA", Nav: "pojia",
			Data: pojiaPageData{Off: true}})
		return
	}
	q := r.URL.Query()
	d := pojiaPageData{
		Query:   strings.TrimSpace(q.Get("q")),
		Class:   strings.TrimSpace(q.Get("class")),
		Program: strings.TrimSpace(q.Get("program")),
		CVE:     strings.TrimSpace(q.Get("cve")),
		Similar: strings.TrimSpace(q.Get("similar")),
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	client := &pojia.Client{BaseURL: s.PojiaURL, Key: s.PojiaKey.Reveal()}
	if d.Query != "" || d.Class != "" || d.Program != "" {
		answer := s.pojiaCall(ctx, client, "search", map[string]any{
			"query": d.Query, "class": d.Class, "program": d.Program, "limit": 20})
		if answer == "" {
			d.Failed = true
		} else {
			d.Results = answer
			d.ResultRows = parsePojiaResults(d.Results)
		}
	}
	if d.Similar != "" {
		answer := s.pojiaCall(ctx, client, "similar", map[string]any{"description": d.Similar})
		if answer == "" {
			d.Failed = true
		} else {
			d.SimilarAnswer = answer
			d.SimilarRows = parsePojiaResults(d.SimilarAnswer)
		}
	}
	if d.CVE != "" {
		d.CVEAnswer = s.pojiaCall(ctx, client, "cve", map[string]any{"cve": d.CVE})
	}
	if d.Class != "" {
		d.Payloads = s.pojiaCall(ctx, client, "payloads", map[string]any{"class": d.Class})
	}
	d.Classes = s.pojiaCall(ctx, client, "classes", nil)
	d.ClassEntries = parsePojiaClasses(d.Classes)
	d.ClassEntries = parsePojiaClasses(d.Classes)
	s.render(w, r, http.StatusOK, "pojia", pageData{Title: "POJIA", Nav: "pojia", Data: d})
}

// pojiaReport is one disclosed report with its annotation.
func (s *server) pojiaReport(w http.ResponseWriter, r *http.Request) {
	if s.PojiaKey.Reveal() == "" {
		http.Redirect(w, r, "/pojia", http.StatusSeeOther)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Redirect(w, r, "/pojia", http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	client := &pojia.Client{BaseURL: s.PojiaURL, Key: s.PojiaKey.Reveal()}
	d := pojiaReportData{ID: id}
	d.Report = s.pojiaCall(ctx, client, "report", map[string]any{"id": id})
	s.render(w, r, http.StatusOK, "pojia-report", pageData{Title: "POJIA report", Nav: "pojia", Data: d})
}

// pojiaCall runs one POJIA tool through the cache: the answer is whatever the
// tool said as text, or the sentence saying why it could not.
func (s *server) pojiaCall(ctx context.Context, c *pojia.Client, kind string, args map[string]any) string {
	tool := "search_reports"
	ttl := 6 * time.Hour
	switch kind {
	case "similar":
		tool = "similar_reports"
		ttl = 24 * time.Hour
	case "cve":
		tool = "cve_lookup"
		ttl = 24 * time.Hour
	case "payloads":
		tool = "get_payloads"
		ttl = 7 * 24 * time.Hour
	case "classes":
		tool = "list_classes"
		ttl = 7 * 24 * time.Hour
	case "report":
		tool = "get_report"
		ttl = 7 * 24 * time.Hour
	}
	key := store.PojiaCacheKey(tool, args)
	if answer, err := s.Store.PojiaCached(ctx, key, ttl); err == nil {
		return answer
	}
	var answer string
	var err error
	switch kind {
	case "similar":
		answer, err = c.SimilarReports(ctx, str(args, "description"))
	case "search":
		answer, err = c.SearchReports(ctx, str(args, "query"), str(args, "class"), str(args, "program"), 20)
	case "cve":
		answer, err = c.CVELookup(ctx, str(args, "cve"))
	case "payloads":
		answer, err = c.GetPayloads(ctx, str(args, "class"))
	case "classes":
		answer, err = c.ListClasses(ctx)
	case "report":
		answer, err = c.GetReport(ctx, str(args, "id"))
	}
	if err != nil {
		s.Log.WarnContext(ctx, "pojia: call failed", "tool", tool, "error", err)
		return ""
	}
	if err := s.Store.SavePojiaCache(ctx, key, answer); err != nil {
		s.Log.WarnContext(ctx, "pojia: cannot cache", "error", err)
	}
	return answer
}

func str(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

type pojiaPageData struct {
	Off           bool
	Query         string
	Class         string
	Program       string
	CVE           string
	Similar       string
	SimilarAnswer string
	Results       string
	CVEAnswer     string
	Payloads      string
	Classes       string
	// Failed says the search call could not be answered.
	Failed bool
	// ClassEntries is the class list parsed, for the chip grid.
	ClassEntries []pojiaClassEntry
	ResultRows   []pojiaResultRow
	SimilarRows  []pojiaResultRow
}

type pojiaReportData struct {
	ID     string
	Report string
}
