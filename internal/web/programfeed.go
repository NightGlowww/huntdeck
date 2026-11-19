package web

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// changeFeed is the changes a program page lists. A program with both
// announcements and other changes gets segments to switch between them,
// so a run of announcements doesn't push scope changes out of view: the
// Overview starts on the other changes, the Changes tab on all of them.
type changeFeed struct {
	Show     string      // "all", "program" or "announcements"
	Segments []scopeLink // none when there's only one kind
	Changes  []store.ChangeView
	Total    int            // changes in Show
	AllTotal int            // every change
	zone     *time.Location // the days a busy day's changes are folded by
}

// feedShows are the feed's segments, in order.
var feedShows = []struct{ key, label string }{{"all", "All"}, {"scope", "Scope changes"}, {"program", "Scope & program"}, {"announcements", "Announcements"}}

// scopeKinds are the changes to a program's scope itself: the entries added,
// removed or edited — what a hunter usually opens the Changes tab for.
var scopeChangeKinds = []string{store.ChangeScopeAdded, store.ChangeScopeRemoved, store.ChangeScopeUpdated}

// programFeed lists up to limit of a program's newest changes for the
// tab, showing show ("" for the tab's default).
func (s *server) programFeed(ctx context.Context, programID int64, tab, show string, limit int) (changeFeed, error) {
	f := changeFeed{Show: "all", zone: s.location(ctx)}
	all := store.ChangeFilter{ProgramID: programID, Newest: true, CollapseFlips: true, Limit: limit}
	var err error
	if f.Changes, f.AllTotal, err = s.Store.Changes(ctx, all); err != nil {
		return f, err
	}
	f.Total = f.AllTotal
	ann := all
	ann.Kinds, ann.Limit = []string{store.ChangeAnnouncement}, 1
	_, announcements, err := s.Store.Changes(ctx, ann)
	if err != nil {
		return f, err
	}
	scope := all
	scope.Kinds, scope.Limit = scopeChangeKinds, 1
	_, scoped, err := s.Store.Changes(ctx, scope)
	if err != nil {
		return f, err
	}
	counts := map[string]int{"all": f.AllTotal, "scope": scoped, "program": f.AllTotal - announcements, "announcements": announcements}
	if counts["program"] == 0 || announcements == 0 {
		return f, nil
	}
	f.Show = oneOf(show, "", "all", "scope", "program", "announcements")
	if f.Show == "" {
		f.Show = "all"
		if tab == "overview" {
			f.Show = "program"
		}
	}
	prefix := "?"
	if tab != "overview" {
		prefix = "?tab=" + tab + "&"
	}
	for _, s := range feedShows {
		if s.key == "scope" && counts["scope"] == 0 {
			continue // nothing scope-shaped to switch to
		}
		f.Segments = append(f.Segments, scopeLink{Key: s.key, Label: s.label, Count: counts[s.key], On: s.key == f.Show,
			URL: prefix + "feed=" + s.key})
	}
	switch f.Show {
	case "scope":
		all.Kinds = scopeChangeKinds
	case "program":
		all.NotKinds = []string{store.ChangeAnnouncement}
	case "announcements":
		all.Kinds = []string{store.ChangeAnnouncement}
	default:
		return f, nil
	}
	f.Changes, f.Total, err = s.Store.Changes(ctx, all)
	return f, err
}

// feedRow is a row of a program's change feed: a change, or several of
// one kind made at once, such as a platform rewording the instructions of
// a dozen assets in one go.
type feedRow struct {
	store.ChangeView
	More []string // the other changes' assets, for a row standing for several
	// Day, for a busy day's row, is that day's rows, which the feed folds
	// under a count of them.
	Day []feedRow
}

// busyDay is how many rows a day has before the feed folds them into one.
const busyDay = 5

// Changes is how many changes the row stands for.
func (r feedRow) Changes() int {
	if len(r.Day) == 0 {
		return 1 + len(r.More)
	}
	n := 0
	for _, d := range r.Day {
		n += d.Changes()
	}
	return n
}

// DaySummary sums up a busy day's changes by kind: "40 added, 20 removed,
// 6 edited".
func (r feedRow) DaySummary() string {
	var added, removed, edited, other int
	for _, d := range r.Day {
		switch d.Kind {
		case store.ChangeScopeAdded:
			added += d.Changes()
		case store.ChangeScopeRemoved:
			removed += d.Changes()
		case store.ChangeScopeUpdated:
			edited += d.Changes()
		default:
			other += d.Changes()
		}
	}
	var parts []string
	for _, p := range []struct {
		n    int
		what string
	}{{added, "scope added"}, {removed, "removed"}, {edited, "edited"}, {other, "other"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%s %s", num(p.n), p.what))
		}
	}
	return strings.Join(parts, ", ")
}

// Text is the row's sentence.
func (r feedRow) Text() string {
	if len(r.More) == 0 {
		return describeChange(r.ChangeView)
	}
	assets := append([]string{assetText(r.Asset)}, r.More...)
	const named = 3
	list := strings.Join(assets[:min(named, len(assets))], ", ")
	if len(assets) > named {
		list += fmt.Sprintf(" and %d more", len(assets)-named)
	}
	return fmt.Sprintf("Scope instructions changed for %d assets: %s", len(assets), list)
}

// Title is the row's every asset, for a tooltip, when it stands for
// several.
func (r feedRow) Title() string {
	if len(r.More) == 0 {
		return ""
	}
	return strings.Join(append([]string{assetText(r.Asset)}, r.More...), "\n")
}

// Rows is the feed's changes with each run of instruction changes from one
// poll as one row, and a day of more than busyDay rows folded into one.
func (f changeFeed) Rows() []feedRow {
	var rows []feedRow
	for _, c := range f.Changes {
		if n := len(rows); n > 0 && groupsWith(rows[n-1].ChangeView, c) {
			rows[n-1].More = append(rows[n-1].More, assetText(c.Asset))
			continue
		}
		rows = append(rows, feedRow{ChangeView: c})
	}
	zone := f.zone
	if zone == nil {
		zone = time.UTC
	}
	day := func(r feedRow) string { return r.OccurredAt.In(zone).Format(time.DateOnly) }
	var out []feedRow
	for i := 0; i < len(rows); {
		j := i + 1
		for j < len(rows) && day(rows[j]) == day(rows[i]) {
			j++
		}
		if j-i > busyDay {
			out = append(out, feedRow{ChangeView: rows[i].ChangeView, Day: rows[i:j]})
		} else {
			out = append(out, rows[i:j]...)
		}
		i = j
	}
	return out
}

// groupsWith reports whether c is one more instruction change of the
// same poll as first.
func groupsWith(first, c store.ChangeView) bool {
	instruction := func(c store.ChangeView) bool {
		return c.Kind == store.ChangeScopeUpdated && c.Field == "instruction" && c.Asset != ""
	}
	return instruction(first) && instruction(c) && first.PollRunID != 0 && c.PollRunID == first.PollRunID && c.ProgramID == first.ProgramID
}
