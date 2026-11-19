package web

import (
	"regexp"
	"strings"
)

// pojiaResultRow is one search hit as the page shows it.
type pojiaResultRow struct {
	ID       string
	Severity string
	Title    string
	Program  string
	Class    string
	URL      string
}

// pojiaResultHead matches the start of one hit: "#1339356 [none] rest…".
var pojiaResultHead = regexp.MustCompile(`^#([0-9]+) \[([a-z?]+)\] (.+)$`)

// parsePojiaResults turns a search answer into rows. Each line is
// "title — program · class · url"; the parts are split on those separators
// rather than one pattern, so a Chinese class name with a dot in it still
// parses. Lines that do not match are left out; the caller shows the raw
// text when nothing parses, so a format change degrades to the old view.
func parsePojiaResults(s string) []pojiaResultRow {
	var out []pojiaResultRow
	for _, line := range strings.Split(s, "\n") {
		m := pojiaResultHead.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		row := pojiaResultRow{ID: m[1], Severity: m[2]}
		rest := m[3]
		if i := strings.Index(rest, " — "); i >= 0 {
			row.Title, rest = strings.TrimSpace(rest[:i]), rest[i+len(" — "):]
		} else {
			row.Title = rest
			out = append(out, row)
			continue
		}
		parts := strings.Split(rest, " · ")
		if len(parts) > 0 {
			row.Program = strings.TrimSpace(parts[0])
		}
		if len(parts) > 1 {
			row.Class = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 {
			row.URL = strings.TrimSpace(parts[len(parts)-1])
		}
		out = append(out, row)
	}
	return out
}
