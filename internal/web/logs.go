package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/logging"
)

type logLevelTab struct {
	Key   string
	Label string
	Count int
	URL   string
}

type logViewEntry struct {
	Seq     int64          `json:"seq"`
	Time    string         `json:"time"`
	ISO     string         `json:"iso"`
	Level   string         `json:"level"`
	Badge   string         `json:"badge"`
	Message string         `json:"message"`
	Attrs   []logging.Attr `json:"attrs,omitempty"`
}

type logsData struct {
	Level     string
	Query     string
	Head      int64
	Counts    logging.LevelCounts
	Entries   []logViewEntry
	Levels    []logLevelTab
	ExportURL string
	ClearURL  string
}

func (s *server) logsPage(w http.ResponseWriter, r *http.Request) {
	if s.ReadOnly || s.Logs == nil {
		s.renderError(w, r, http.StatusNotFound, "Page not found.")
		return
	}
	level := normalizeLogLevelFilter(r.URL.Query().Get("level"))
	qText := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := parseLogLimit(r.URL.Query().Get("limit"), 250)

	entries, head := s.Logs.Query(logging.Query{
		Level: level,
		Text:  qText,
		Limit: limit,
	})

	if r.URL.Query().Get("format") == "txt" {
		s.writeLogsText(w, entries)
		return
	}

	counts := s.Logs.Counts()
	viewEntries := s.formatLogEntries(entries)
	levels := []logLevelTab{
		{Key: "all", Label: "All", Count: counts.Total, URL: logsFilterURL("all", qText)},
		{Key: "error", Label: "Error", Count: counts.Error, URL: logsFilterURL("error", qText)},
		{Key: "warn", Label: "Warn", Count: counts.Warn, URL: logsFilterURL("warn", qText)},
		{Key: "info", Label: "Info", Count: counts.Info, URL: logsFilterURL("info", qText)},
		{Key: "debug", Label: "Debug", Count: counts.Debug, URL: logsFilterURL("debug", qText)},
	}

	data := logsData{
		Level:     level,
		Query:     qText,
		Head:      head,
		Counts:    counts,
		Entries:   viewEntries,
		Levels:    levels,
		ExportURL: logsExportURL(level, qText),
		ClearURL:  "/logs/clear",
	}
	s.render(w, r, http.StatusOK, "logs", pageData{Title: "Logs", Nav: "logs", Data: data})
}

func (s *server) logsTail(w http.ResponseWriter, r *http.Request) {
	if s.ReadOnly || s.Logs == nil {
		writeJSONError(w, http.StatusNotFound, "logs unavailable")
		return
	}
	level := normalizeLogLevelFilter(r.URL.Query().Get("level"))
	qText := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := parseLogLimit(r.URL.Query().Get("limit"), 250)

	entries, head := s.Logs.Query(logging.Query{
		Level: level,
		Text:  qText,
		Limit: limit,
	})

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		Head    int64               `json:"head"`
		Counts  logging.LevelCounts `json:"counts"`
		Entries []logViewEntry      `json:"entries"`
	}{
		Head:    head,
		Counts:  s.Logs.Counts(),
		Entries: s.formatLogEntries(entries),
	})
}

func (s *server) logsClear(w http.ResponseWriter, r *http.Request) {
	if s.ReadOnly || s.Logs == nil {
		s.renderError(w, r, http.StatusNotFound, "Page not found.")
		return
	}
	s.Logs.Clear()
	http.Redirect(w, r, "/logs", http.StatusSeeOther)
}

func (s *server) formatLogEntries(raw []logging.Entry) []logViewEntry {
	loc := s.shownZone()
	if loc == nil {
		loc = time.UTC
	}
	out := make([]logViewEntry, 0, len(raw))
	for _, e := range raw {
		out = append(out, logViewEntry{
			Seq:     e.Seq,
			Time:    e.Time.In(loc).Format("2006-01-02 15:04:05"),
			ISO:     e.Time.UTC().Format(time.RFC3339),
			Level:   e.Level,
			Badge:   logLevelBadge(e.Level),
			Message: e.Message,
			Attrs:   e.Attrs,
		})
	}
	return out
}

func (s *server) writeLogsText(w http.ResponseWriter, entries []logging.Entry) {
	loc := s.shownZone()
	if loc == nil {
		loc = time.UTC
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="huntdeck.log"`)
	w.Header().Set("Cache-Control", "no-store")
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%s [%s] %s", e.Time.In(loc).Format(time.RFC3339), logLevelBadge(e.Level), e.Message)
		for _, a := range e.Attrs {
			fmt.Fprintf(&b, " %s=%s", a.Key, a.Value)
		}
		b.WriteByte('\n')
	}
	_, _ = w.Write([]byte(b.String()))
}

func normalizeLogLevelFilter(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "error", "warn", "info", "debug":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "all"
	}
}

func parseLogLimit(raw string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return def
	}
	return min(n, 1000)
}

func logLevelBadge(lvl string) string {
	switch lvl {
	case "error":
		return "ERR"
	case "warn":
		return "WRN"
	case "debug":
		return "DBG"
	default:
		return "INF"
	}
}

func logsFilterURL(level, q string) string {
	v := url.Values{}
	if level != "" && level != "all" {
		v.Set("level", level)
	}
	if q != "" {
		v.Set("q", q)
	}
	if len(v) == 0 {
		return "/logs"
	}
	return "/logs?" + v.Encode()
}

func logsExportURL(level, q string) string {
	v := url.Values{}
	v.Set("format", "txt")
	if level != "" && level != "all" {
		v.Set("level", level)
	}
	if q != "" {
		v.Set("q", q)
	}
	return "/logs?" + v.Encode()
}
