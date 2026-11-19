package web

import (
	"html"
	"html/template"
	"strings"
)

// zeroWidthSpace is a break opportunity that renders as nothing.
const zeroWidthSpace = "\u200b"

// softBreak puts a break opportunity after the punctuation a URL uses, so a
// long one wraps at a boundary the reader can follow rather than mid-word.
func softBreak(s string) template.HTML {
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(r)
		switch r {
		case '/', '.', '-', '_', '?', '&', '=':
			b.WriteString(zeroWidthSpace)
		}
	}
	return template.HTML(html.EscapeString(b.String())) // #nosec G203 -- escaped just above
}
