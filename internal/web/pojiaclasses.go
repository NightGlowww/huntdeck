package web

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

// pojiaClassLine matches one line of POJIA's class list: "xss — 跨站脚本 ...".
var pojiaClassLine = regexp.MustCompile(`(?m)^(\w+) — `)

// pojiaClasses makes the class list navigable: each class key becomes a link
// that searches it here. The rest of the text is escaped.
func pojiaClasses(s string) template.HTML {
	var b strings.Builder
	last := 0
	for _, m := range pojiaClassLine.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(html.EscapeString(s[last:m[0]]))
		key := s[m[2]:m[3]]
		b.WriteString(`<a class="link" href="/pojia?class=` + key + `">` + key + `</a>`)
		last = m[1]
	}
	b.WriteString(html.EscapeString(s[last:]))
	return template.HTML(b.String()) // #nosec G203 -- escaped above
}
