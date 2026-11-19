package web

import (
	"regexp"
	"strings"
)

// cssRule is one declaration block: the selector as written, where it sits, the
// media query around it (empty when unconditional) and the properties with
// their values. A stylesheet may declare the same selector more than once, so
// callers get a list, not a map.
type cssRule struct {
	sel   string
	file  string
	media string
	props map[string]string
}

var cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
var cssProp = regexp.MustCompile(`(?m)([a-zA-Z-]+)\s*:\s*([^;}]*)`)

// parseCSS returns one entry per declaration block, in source order. It walks
// the text once, keeping the prelude of the enclosing @media (if any) and
// jumping over each rule's body so its closing brace is never mistaken for the
// end of a media block.
func parseCSS(src, file string) []cssRule {
	src = cssComment.ReplaceAllString(src, "")
	var out []cssRule
	var media []string
	var buf strings.Builder
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '}':
			if len(media) > 0 {
				media = media[:len(media)-1]
			}
			buf.Reset()
		case c != '{':
			buf.WriteByte(c)
		default:
			prelude := strings.TrimSpace(buf.String())
			buf.Reset()
			if strings.HasPrefix(prelude, "@") {
				media = append(media, prelude)
				continue
			}
			end := strings.IndexByte(src[i:], '}')
			if end < 0 {
				return out
			}
			body := src[i+1 : i+end]
			props := map[string]string{}
			for _, m := range cssProp.FindAllStringSubmatch(body, -1) {
				props[strings.TrimSpace(m[1])] = strings.Join(strings.Fields(m[2]), " ")
			}
			for _, sel := range strings.Split(prelude, ",") {
				sel = strings.TrimSpace(sel)
				if sel == "" {
					continue
				}
				out = append(out, cssRule{sel: sel, file: file, media: strings.Join(media, " / "), props: props})
			}
			i += end // land on this rule's own brace, which the loop skips
		}
	}
	return out
}
