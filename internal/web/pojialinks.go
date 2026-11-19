package web

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

// pojiaReportID matches a report reference in POJIA's answers: "#1339356".
var pojiaReportID = regexp.MustCompile(`#([0-9]{4,10})`)

// pojiaLinks makes a POJIA answer navigable: every "#123456" report id
// becomes a link to its page here, where the annotation (how it was found,
// the payloads, the fix) is one click away. The rest of the text is escaped;
// POJIA's own words are never interpreted as markup.
func pojiaLinks(s string) template.HTML {
	var b strings.Builder
	last := 0
	for _, m := range pojiaReportID.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(html.EscapeString(s[last:m[0]]))
		id := s[m[2]:m[3]]
		b.WriteString(`<a class="link" href="/pojia/report?id=` + id + `">#` + id + `</a>`)
		last = m[1]
	}
	b.WriteString(html.EscapeString(s[last:]))
	return template.HTML(b.String()) // #nosec G203 -- every other byte is escaped above
}
