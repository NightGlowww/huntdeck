package web

import (
	"regexp"
	"strconv"
	"strings"
)

// pojiaClassEntry is one vulnerability class as the page shows it.
type pojiaClassEntry struct {
	Key   string
	Label string
	Count int
}

// pojiaClassLineRx pulls key, label and count out of POJIA's list, one
// class per line: "xss — 跨站脚本 (Cross-site Scripting): 2498 reports, …".
var pojiaClassLineRx = regexp.MustCompile(`(?m)^(\w+) — (.+?): ([0-9,]+) reports`)

// parsePojiaClasses turns the class list into entries the page can lay out
// as a grid of chips instead of a wall of text.
func parsePojiaClasses(s string) []pojiaClassEntry {
	var out []pojiaClassEntry
	for _, m := range pojiaClassLineRx.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(strings.ReplaceAll(m[3], ",", ""))
		out = append(out, pojiaClassEntry{Key: m[1], Label: m[2], Count: n})
	}
	return out
}
