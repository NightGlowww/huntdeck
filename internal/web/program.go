package web

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/huntscore"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// programData is a program's page. The head and the headline numbers
// show on every tab; the rest is what the tab (Tab) needs.
type programData struct {
	P *store.ProgramDetail
	// StateSince is when it went paused or closed; zero if not known.
	StateSince time.Time
	Tab        string // "overview", "scope", "policy", "stats" or "changes"
	// Tabs are the page's tabs; ones with nothing to show are left out.
	Tabs []programTab
	// Row is the program as the Programs list has it, for the headline
	// numbers, which repeat the list's cells.
	Row programRow
	// HuntChange says how the HuntScore moved over its history, "▼ 3",
	// and HuntSince in full: "down 3 since Oct 6"; "" without two days.
	HuntChange, HuntSince string
	// Before is what to know before testing: requirements and the rules
	// for test traffic.
	Before []testRule
	// AIRules are what a model read out of the same policy, when one was
	// configured and had run; AIModel and AIAt say which and when.
	AIRules []store.AIRule
	AIModel string
	AIAt    time.Time
	Latest  *store.UserStatsSnapshot // the user's own numbers, if the platform shares them

	InScope, OutOfScope, Removed []store.EntryView
	Wildcards, Domains           []string
	// Identifiers is every in-scope target (host, URL, IP or range), for
	// "Copy all targets": app names and other assets aren't for tools.
	Identifiers []string
	// Roots are the root (registrable) domains of the in-scope wildcards,
	// domains and URLs, for recon: a root often isn't in scope itself.
	Roots []string
	// OutTargets is every out-of-scope target, for "Copy out of scope":
	// what to keep tools away from.
	OutTargets []string
	// BountyEntries counts in-scope entries eligible for bounties.
	BountyEntries int

	// Feed is the newest changes: a few on the Overview, up to
	// changesShown on the Changes tab.
	Feed changeFeed

	// Fresh are the newest in-scope entries added in the last
	// store.FreshWindow (the Overview's "New in scope"), FreshTotal how many
	// there are.
	Fresh      []store.EntryView
	FreshTotal int

	Scope scopeView   // the Scope tab
	Docs  []policyDoc // the Policy tab, and the tab's contents list

	// Resolved describes resolved, undisclosed reports (HackerOne);
	// Accepted accepted ones (Bugcrowd).
	Resolved resolutionView
	Accepted acceptedView
	// Disclosed are the newest publicly disclosed reports (a few on the
	// Overview, up to disclosuresShown on Stats), DisclosedSum describes
	// all of them.
	Disclosed    []store.DisclosureView
	DisclosedSum store.DisclosureSummary
	// History charts the platform's own numbers over every day saved (the
	// Stats tab).
	History []historyChart
	// Note is the hunter's own note on this program (the Notes tab), and
	// NoteAt when it was last saved: HuntDeck keeps it, not the browser.
	Note   string
	NoteAt time.Time
	// NoteErr says the stored note could not be read: the tab refuses to
	// open an empty editor, because saving it would overwrite the real text.
	NoteErr bool
	// Pojia is the disclosed-report history panel; PojiaEnabled says a key is
	// configured, PojiaStale that the answer shown is older than a week.
	Pojia        *store.PojiaProfile
	PojiaEnabled bool
	// PolicyCheckOn says a decision model is configured, so the Policy tab
	// offers the check. PolicyCall, PolicyP and PolicyWhy carry its answer back.
	PolicyCheckOn bool
	PolicyCall    string
	PolicyP       string
	PolicyRule    string
	PolicyQuote   string
	PolicyWhy     string
	PolicyError   string
	// PojiaFailed says the fetch failed and nothing is cached, so the panel
	// says that rather than hiding.
	PojiaFailed bool
	PojiaStale  bool
	// Mine is the user's own reports to the program (the Overview); nil
	// without any, and always in a read-only copy.
	Mine *programMine
	// MailOn: HuntMail is set up, so the Email tab shows; Mail is the tab.
	MailOn bool
	Mail   *programMailView
	// SignalCheck compares HackerOne's minimum Signal requirement with the user's past-year Signal.
	SignalCheck *signalCautionView
}

// programTab is one of a program page's tabs.
type programTab struct {
	Key, Label string
	Count      int
	Counted    bool // show Count
	On         bool
	Notes      bool // the notes tab
}

// programTabs are a program page's tabs, in order.
var programTabs = []string{"overview", "scope", "policy", "stats", "changes", "email", "notes"}

const (
	// disclosuresShown is how many disclosed reports a program page lists.
	disclosuresShown = 200
	// changesShown is how many changes the Changes tab lists; the
	// Overview lists recentChanges.
	changesShown  = 200
	recentChanges = 6
	// recentDisclosed is how many disclosed reports the Overview lists, and
	// freshShown how many newly added assets.
	recentDisclosed = 5
	freshShown      = 8
)

// newestFirst sorts entries by the time at, newest first; entries without
// one keep their order at the end.
func newestFirst(entries []store.EntryView, at func(store.EntryView) *time.Time) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := at(entries[i]), at(entries[j])
		return a != nil && (b == nil || a.After(*b))
	})
}

func (s *server) program(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := s.Store.Program(ctx, canonicalPlatform(r.PathValue("platform")), r.PathValue("handle"))
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "No such program.")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	now := time.Now()
	tab := r.URL.Query().Get("tab")
	if tab == "reports" {
		tab = "stats" // its name until 2026-11-05; old links still work
	}
	if tab == "stats" && p.Platform == store.Independent {
		// A program added by hand has no stats: its Overview has what there is.
		q := r.URL.Query()
		q.Del("tab")
		http.Redirect(w, r, (&url.URL{Path: r.URL.Path, RawQuery: q.Encode()}).String(), http.StatusMovedPermanently)
		return
	}
	d := programData{P: p, Tab: oneOf(tab, programTabs...), MailOn: s.Mail != nil}
	if d.Tab == "email" && !d.MailOn {
		d.Tab = "overview"
	}
	// The policy check needs a decision model; its answer comes back on the query.
	d.PolicyCheckOn = s.JevKey.Reveal() != ""
	q := r.URL.Query()
	d.PolicyCall = q.Get("policy_call")
	d.PolicyP = q.Get("policy_p")
	d.PolicyRule = q.Get("policy_rule")
	d.PolicyQuote = q.Get("policy_quote")
	d.PolicyWhy = q.Get("policy_why")
	d.PolicyError = q.Get("policy_error")

	// POJIA's disclosed-report history: cached for a week, fetched on the first
	// visit after that. A missing key leaves the panel off entirely.
	if s.PojiaKey.Reveal() != "" {
		s.pojiaPanel(ctx, r, p, &d)
	}
	if p.State != "open" {
		since, err := s.Store.StateSince(ctx, p.ID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if since != nil {
			d.StateSince = *since
		}
	}
	d.splitEntries()
	// The Notes tab's text and when it was last saved: kept by HuntDeck, so it
	// survives a browser or a second device.
	if note, at, err := s.Store.ProgramNote(ctx, p.ID); err == nil {
		d.Note, d.NoteAt = note, at
	} else {
		// An unreadable note must not look like an empty one: the editor would
		// open blank and the next save would overwrite what is stored.
		s.Log.WarnContext(ctx, "program: cannot read the note", "program", p.Handle, "error", err)
		d.NoteErr = true
	}
	// The Scope tab's copy link: these rows as plain text, before anything
	// else is loaded.
	if r.URL.Query().Get("format") == "txt" && oneOf(tab, programTabs...) == "scope" {
		// The page shows the first screenful; the export is everything the
		// filter matches.
		q := r.URL.Query()
		q.Set("all", "1")
		r.URL.RawQuery = q.Encode()
		s.writeScopeText(w, d.scopeView(r))
		return
	}
	if n := len(p.Stats); n > 0 {
		d.Latest = &p.Stats[n-1]
	}
	d.Before = beforeYouTest(p)
	d.SignalCheck = s.programSignalCheck(ctx, p)
	// A model reading, when one was cached for this exact policy text. It is
	// advisory: the rules above quote the policy, this one paraphrases it.
	if pol := strings.TrimSpace(p.Policy); pol != "" {
		if r, err := s.Store.AIReading(ctx, p.ID, store.PolicyHash(pol)); err == nil && r != nil {
			d.AIRules, d.AIModel, d.AIAt = r.Rules, r.Model, r.At
		}
	}
	d.Docs = policyDocs(p)

	items, _, err := s.Store.Programs(ctx, store.ProgramFilter{ID: p.ID})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if len(items) > 0 {
		rows, err := s.programRows(ctx, items, now)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		d.Row = rows[0]
	}
	hist, err := s.Store.HuntHistory(ctx, p.ID, now.Add(-huntscore.Window))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	d.HuntChange, d.HuntSince = huntChange(hist)

	limit := recentChanges
	if d.Tab == "changes" {
		limit = changesShown
	}
	if d.Feed, err = s.programFeed(ctx, p.ID, d.Tab, r.URL.Query().Get("feed"), limit); err != nil {
		s.serverError(w, r, err)
		return
	}
	if d.Disclosed, d.DisclosedSum, err = s.Store.ProgramDisclosures(ctx, p.ID, disclosuresShown); err != nil {
		s.serverError(w, r, err)
		return
	}
	if d.Tab != "stats" && len(d.Disclosed) > recentDisclosed {
		d.Disclosed = d.Disclosed[:recentDisclosed]
	}
	if p.PublicStats != nil { // on every tab: it decides whether Stats shows
		hist, err := s.Store.ProgramStatsHistory(ctx, p.ID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		d.History = historyCharts(hist, p.OffersBounty)
	}
	sum, err := s.Store.ProgramResolutions(ctx, p.ID, now, resolutionMonths)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	d.Resolved = buildResolutionView(sum, p.Currency)
	acc, err := s.Store.ProgramAccepted(ctx, p.ID, now, resolutionMonths)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	d.Accepted = buildAcceptedView(acc, p.Currency)
	if d.Tab == "scope" {
		d.Scope = d.scopeView(r)
	}
	if d.Tab == "email" {
		made, _ := strconv.ParseInt(r.URL.Query().Get("made"), 10, 64)
		if d.Mail, err = s.programMail(ctx, p, made, nlParam(r.URL.Query())); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	if d.Tab == "overview" {
		if d.Mine, err = s.programMine(ctx, p); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	d.Tabs = d.tabs()
	s.render(w, r, http.StatusOK, "program", pageData{Title: p.Name, Nav: "programs", Description: programDescription(d),
		Feed: programPath(p) + "/changes.atom", FeedTitle: "HuntDeck: " + p.Name + " changes",
		Image: programImage(p.ProgramRef), ImageAlt: p.Name + " on " + platformLabel(p.Platform) + ": HuntScore, bounty range, total paid and scope",
		SearchTitle: programTitle(d), Crumbs: []crumb{{"Programs", "/programs"}, {p.Name, programPath(p)}}, Data: d})
}

// splitEntries sorts the program's entries into in scope, out of scope
// and removed, and lists the in-scope targets to copy.
func (d *programData) splitEntries() {
	seen := map[string]bool{}
	for _, e := range d.P.Entries {
		switch {
		case e.RemovedAt != nil:
			d.Removed = append(d.Removed, e)
		case e.InScope:
			d.InScope = append(d.InScope, e)
			if e.BountyEligible {
				d.BountyEntries++
			}
			for _, t := range e.Targets {
				if seen[t.Kind+t.Value] {
					continue
				}
				seen[t.Kind+t.Value] = true
				if slices.Contains(scope.Kinds, t.Kind) {
					d.Identifiers = append(d.Identifiers, t.Value)
				}
				if t.Apex != "" && slices.Contains([]string{scope.KindWildcard, scope.KindDomain, scope.KindURL}, t.Kind) &&
					!slices.Contains(d.Roots, t.Apex) {
					d.Roots = append(d.Roots, t.Apex)
				}
				switch t.Kind {
				case scope.KindWildcard:
					d.Wildcards = append(d.Wildcards, t.Value)
				case scope.KindDomain:
					d.Domains = append(d.Domains, t.Value)
				}
			}
		default:
			d.OutOfScope = append(d.OutOfScope, e)
			for _, t := range e.Targets {
				if slices.Contains(scope.Kinds, t.Kind) && !slices.Contains(d.OutTargets, t.Value) {
					d.OutTargets = append(d.OutTargets, t.Value)
				}
			}
		}
	}
	newestFirst(d.Removed, func(e store.EntryView) *time.Time { return e.RemovedAt })

	// New in scope: added by a known date (not "by then", where the
	// platform's history starts) within the fresh window.
	since := time.Now().Add(-store.FreshWindow)
	for _, e := range d.InScope {
		if e.AddedAt != nil && !e.AddedBound && e.AddedAt.After(since) {
			d.Fresh = append(d.Fresh, e)
		}
	}
	newestFirst(d.Fresh, func(e store.EntryView) *time.Time { return e.AddedAt })
	d.FreshTotal = len(d.Fresh)
	if len(d.Fresh) > freshShown {
		d.Fresh = d.Fresh[:freshShown]
	}
}

// tabs lists the page's tabs: Policy only with a policy, Stats only with
// reports or stats over time to show.
func (d *programData) tabs() []programTab {
	out := []programTab{
		{Key: "overview", Label: "Overview"},
		{Key: "scope", Label: "Scope", Count: len(d.InScope), Counted: true},
	}
	if len(d.Docs) > 0 {
		out = append(out, programTab{Key: "policy", Label: "Policy"})
	}
	if d.hasReports() || len(d.History) > 0 {
		out = append(out, programTab{Key: "stats", Label: "Stats"})
	}
	out = append(out, programTab{Key: "changes", Label: "Changes", Count: d.Feed.AllTotal, Counted: true})
	if d.MailOn {
		out = append(out, programTab{Key: "email", Label: "Email"})
	}
	out = append(out, programTab{Key: "notes", Label: "Notes", Notes: true}) // always has a box: HuntDeck keeps the notes
	for i := range out {
		out[i].On = out[i].Key == d.Tab
	}
	return out
}

// hasReports says whether there are resolved, accepted or disclosed
// reports to show.
func (d *programData) hasReports() bool {
	return d.Resolved.Total > 0 || d.Resolved.HistoryFetch != nil || d.Accepted.Total > 0 || d.Accepted.Since != nil ||
		d.Accepted.SyncedAt != nil || d.DisclosedSum.Total > 0
}

// huntChange says how a HuntScore moved over its daily history, short
// ("▲ 3") and in full ("up 3 since Oct 6").
func huntChange(days []store.HuntDay) (short, long string) {
	if len(days) < 2 {
		return "", ""
	}
	diff := math.Round(days[len(days)-1].Score - days[0].Score)
	since := " since " + days[0].Day.UTC().Format("Jan 2")
	switch {
	case diff > 0:
		return fmt.Sprintf("▲ %.0f", diff), fmt.Sprintf("up %.0f", diff) + since
	case diff < 0:
		return fmt.Sprintf("▼ %.0f", -diff), fmt.Sprintf("down %.0f", -diff) + since
	}
	return "no change", "no change" + since
}

// setProgramNote saves the Notes tab: the hunter's own notes on a program,
// kept in HuntDeck now rather than in one browser.
func (s *server) setProgramNote(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := s.Store.Program(ctx, canonicalPlatform(r.PathValue("platform")), r.PathValue("handle"))
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "No such program.")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Bad request.")
		return
	}
	if err := s.Store.SetProgramNote(ctx, p.ID, r.PostFormValue("note")); err != nil {
		s.serverError(w, r, err)
		return
	}
	//nolint:gosec // the path the request already reached, plus a tab and a flash
	http.Redirect(w, r, r.URL.Path+"?tab=notes&msg=note-saved", http.StatusSeeOther)
}
