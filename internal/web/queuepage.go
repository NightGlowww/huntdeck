package web

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/huntqueue"
	"github.com/mdpsec/huntdeck/internal/store"
)

// queueData is the page: what to hunt today, and what is blocked and why.
// QueueCutsNeeded is the number of point-in-time cuts the backtest in
// docs/design/ai-layer.md asks for before HuntQueue may replace HuntScore.
const QueueCutsNeeded = 8

type queueData struct {
	Rows       []queueRowView
	Gated      []queueRowView
	ComputedAt time.Time
	Stale      bool
	Filter     store.QueueFilter
	Platforms  []string
	// Cuts is how many days of HuntScore history exist; the backtest needs
	// QueueCutsNeeded before it can judge the ranking.
	Cuts int
	// BelowFloor is how many targets the rebuild left out for scoring under
	// the floor: the page says so, so a short list does not read as complete.
	BelowFloor int
	Pager      pager
	// Total is every ranked target, not just this page: the heading said "50
	// targets" when the queue held 207.
	Total int
	// IgnoredWindow is an `added` value that did not parse: the filter was not
	// applied, and saying so is better than showing the whole queue as if it had
	// been.
	IgnoredWindow string
	// GatedTotal is how many targets the queue keeps out, however many this
	// page lists, and GatedBy breaks that down by gate.
	GatedTotal int
	GatedBy    []store.QueueGate
	// Chips are the filter toggles, each keeping the others.
	Chips []queueChip
}

type queueRowView struct {
	Platform string
	Handle   string
	Kind     string
	Value    string
	Score    float64
	Reasons  []string
	// Factors is the score breakdown: what each input did to it. LossName and
	// LossPct name the factor that cost the most, for one line above the list.
	Factors  []huntqueue.Factor
	LossName string
	LossPct  int
	Gate     string // rendered reason for a gated row
}

// queuePage shows HuntQueue: targets ranked by whether they can be started
// today and what the work is worth per hour. It is derived data, rebuilt every
// half hour, so the page says when it was last built.
func (s *server) queuePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	// A window that does not parse is not a filter: say so rather than showing
	// the whole queue as if the filter had been applied.
	addedWindow, ignored := q.Get("added"), ""
	if addedWindow != "" {
		if _, ok := store.AddedDays(addedWindow); !ok {
			ignored, addedWindow = addedWindow, ""
		}
	}
	filter := store.QueueFilter{
		Platform:      q.Get("platform"),
		Kind:          q.Get("kind"),
		Paying:        q.Get("paying") == "1",
		Added:         addedWindow,
		OnePerProgram: q.Get("one") == "1",
	}
	const perPage = 50
	page := pageFrom(r)
	// The build time comes from the table itself, not from the rows: a filter
	// that matches nothing must not look like a queue that was never built.
	total, _, err := s.Store.HuntQueueCount(ctx, filter)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	computed, err := s.Store.HuntQueueBuiltAt(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	rows, _, err := s.Store.HuntQueueTop(ctx, filter, perPage, (page-1)*perPage)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	gated, err := s.Store.HuntQueueGated(ctx, filter, 20)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	d := queueData{ComputedAt: computed, Stale: time.Since(computed) > 2*time.Hour, Filter: filter, Platforms: s.platformNames()}
	d.Chips = queueChips(q, filter, s.platformNames())
	d.IgnoredWindow = ignored
	if days, err := s.Store.ScoreDays(ctx); err == nil {
		d.Cuts = len(days)
	}
	d.Pager = newPager(r, perPage, total)
	d.Total = total
	if n, err := s.Store.HuntQueueGatedCount(ctx); err == nil {
		d.GatedTotal = n
	}
	if counts, err := s.Store.HuntQueueGateCounts(ctx); err == nil {
		for gate, n := range counts {
			d.GatedBy = append(d.GatedBy, store.QueueGate{Gate: gate, Count: n})
		}
		sort.Slice(d.GatedBy, func(i, j int) bool { return d.GatedBy[i].Count > d.GatedBy[j].Count })
	}
	// The reasons arrive as message keys; the page renders them in its language.
	t := func(msg string, args ...string) string {
		out := s.T(r, msg)
		for _, a := range args {
			out = strings.Replace(out, "%s", a, 1)
		}
		return out
	}
	d.Rows = s.queueViews(r, rows)
	for _, row := range gated {
		// A gated row carries its gate reason, but indexing a slice that could
		// be empty is not worth a panic in a page.
		rendered := huntqueue.Render(row.Reasons, t)
		gate := row.Gate
		if len(rendered) > 0 {
			gate = rendered[0]
		}
		d.Gated = append(d.Gated, queueRowView{
			Platform: row.Platform, Handle: row.Handle, Kind: row.Kind, Value: row.Value,
			Gate: gate,
		})
	}
	if n, err := s.Store.QueueDropped(ctx); err == nil {
		d.BelowFloor = n
	} else {
		// The line is absent either way; the log tells the two apart.
		s.Log.WarnContext(ctx, "queue: cannot read the left-out count", "error", err)
	}
	s.render(w, r, http.StatusOK, "queue", pageData{Title: "Hunt queue", Nav: "queue", Data: d})
}

// queueViews renders queue rows for a page: reasons translated, the "nothing to
// arrange" default dropped because it is noise on every row.
func (s *server) queueViews(r *http.Request, rows []store.HuntQueueRow) []queueRowView {
	t := func(msg string, args ...string) string {
		out := s.T(r, msg)
		for _, a := range args {
			out = strings.Replace(out, "%s", a, 1)
		}
		return out
	}
	out := make([]queueRowView, 0, len(rows))
	for _, row := range rows {
		kept := make([]huntqueue.Reason, 0, len(row.Reasons))
		for _, reason := range row.Reasons {
			if reason.Key != "nothing to arrange: start now" {
				kept = append(kept, reason)
			}
		}
		loss := biggestLoss(row.Factors)
		out = append(out, queueRowView{
			Platform: row.Platform, Handle: row.Handle, Kind: row.Kind, Value: row.Value,
			Score: row.Score, Reasons: huntqueue.Render(kept, t), Factors: row.Factors, LossName: loss.Name, LossPct: loss.Pct,
		})
	}
	return out
}

// queueChip is one filter chip: its label, the address that toggles it, and
// whether it is on. The addresses keep the other filters, so two chips can be
// combined (the links used to reset everything else).
type queueChip struct {
	Label string
	Href  string
	On    bool
	// Clear marks the one chip that drops every filter.
	Clear bool
}

func queueChips(q url.Values, f store.QueueFilter, platforms []string) []queueChip {
	with := func(key, value string) string {
		v := url.Values{}
		for k, vals := range q {
			if k == "page" || k == key {
				continue
			}
			v[k] = vals
		}
		if value != "" && value != "0" {
			v.Set(key, value)
		}
		if enc := v.Encode(); enc != "" {
			return "/queue?" + enc
		}
		return "/queue"
	}
	toggle := func(key, current, on, off string) string {
		if current == on {
			return with(key, off)
		}
		return with(key, on)
	}
	chips := []queueChip{
		{Label: "All platforms", Href: with("platform", ""), On: f.Platform == ""},
		{Label: "Paying only", Href: toggle("paying", boolParam(f.Paying), "1", ""), On: f.Paying},
		{Label: "Added in 7 days", Href: toggle("added", f.Added, "7d", ""), On: f.Added == "7d"},
		{Label: "One per program", Href: toggle("one", boolParam(f.OnePerProgram), "1", ""), On: f.OnePerProgram},
	}
	for _, p := range platforms {
		chips = append(chips, queueChip{Label: p, Href: with("platform", p), On: f.Platform == p})
	}
	// One way back out: several chips can be on at once, and turning each off to
	// clear the page is the kind of small chore that makes filters feel heavy.
	if f.Platform != "" || f.Paying || f.Added != "" || f.OnePerProgram {
		chips = append(chips, queueChip{Label: "Clear filters", Href: "/queue", Clear: true})
	}
	return chips
}

func boolParam(b bool) string {
	if b {
		return "1"
	}
	return ""
}

// queueLoss names the factor that cost a row the most: the one to look at first
// when the score is lower than expected.
type queueLoss struct {
	Name string
	Pct  int
}

func biggestLoss(factors []huntqueue.Factor) queueLoss {
	best := queueLoss{}
	for _, f := range factors {
		if f.Name == "personal" {
			continue // a prior on the reader, not a property of the target
		}
		pct := int((1-f.Effect)*100 + 0.5)
		if pct > best.Pct {
			best = queueLoss{Name: f.Name, Pct: pct}
		}
	}
	return best
}
