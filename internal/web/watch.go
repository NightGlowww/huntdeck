package web

import (
	"strings"

	"github.com/mdpsec/huntdeck/internal/store"
)

// The watchlist: programs a visitor watches, kept in their browser
// (watch.js, localStorage), so it needs no account and works on a
// read-only copy such as the demo. Pages get it as ?watch=, programs as
// platform/handle separated by commas: Programs and Changes (and its feed)
// keep to those programs. The rail's "Your watchlist" choice is hidden
// until watch.js finds a list to link to.

// watchMax is the most programs a watchlist link keeps to.
const watchMax = 200

// watchParam reads ?watch=: well-formed platform/handle pairs, once each,
// at most watchMax.
func watchParam(v string) []store.ProgramKey {
	var out []store.ProgramKey
	seen := map[store.ProgramKey]bool{}
	for part := range strings.SplitSeq(v, ",") {
		pf, handle, ok := strings.Cut(strings.TrimSpace(part), "/")
		if !ok || pf == "" || handle == "" || len(handle) > 200 || strings.ContainsAny(handle, "/?#") {
			continue
		}
		k := store.ProgramKey{Platform: canonicalPlatform(pf), Handle: handle}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
		if len(out) == watchMax {
			break
		}
	}
	return out
}

// watchKey is a watchlist as a memo key's part.
func watchKey(keys []store.ProgramKey) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k.Platform + "/" + k.Handle
	}
	return strings.Join(parts, ",")
}

// watchGroup is the rail's choices from what this browser keeps: the
// watchlist (?watch=) and, with notes, the programs it has notes on
// (?noted=). Each is on while its parameter keeps the list to
// it (choosing it again drops that), else hidden for the scripts to show
// and link when the browser has some.
func watchGroup(l listQuery, watchOn, notedOn, notes bool) railGroup {
	g := railGroup{Title: "In this browser", Watch: true, On: watchOn || notedOn, Options: []railOption{
		{Label: "Your watchlist", Title: "Programs you watch, saved in this browser", On: watchOn, URL: l.with("watch", ""), Watch: true}}}
	if notes {
		g.Options = append(g.Options, railOption{Label: "Has notes", Title: "Programs you have notes on, saved in this browser",
			On: notedOn, URL: l.with("noted", ""), Noted: true})
	}
	return g
}
