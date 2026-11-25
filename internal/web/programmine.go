package web

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/mdpsec/huntdeck/internal/reports"
	"github.com/mdpsec/huntdeck/internal/store"
)

// A program page's "Your reports" panel: the user's own reports to the
// program, what they earned, and the user's times against the ones the
// program publishes. Only when there are some, and never in a read-only
// copy.

// mineShown is how many of the user's reports the panel lists.
const mineShown = 5

type programMine struct {
	reports.ProgramRow
	Open       int
	Decided    int
	Acceptance float64
	Recent     []reportRow
	AllURL     string
	Times      []mineTime
}

// mineTime is one of the user's times beside the program's.
type mineTime struct {
	Label, Title string
	Yours        reports.Stat
	Theirs       *float64
}

// Pct shows the acceptance, or a dash when nothing is decided.
func (programMine) Pct(f float64) string { return analyticsView{}.Pct(f) }

// Mine and Program show the user's time and the program's, or "".
func (t mineTime) Mine() string    { return analyticsView{}.Hours(t.Yours) }
func (t mineTime) Program() string { return analyticsView{}.Published(t.Theirs) }

// Versus says whether the user's time is quicker ("good") or slower ("bad").
func (t mineTime) Versus() string { return analyticsView{}.Versus(t.Yours, t.Theirs) }

// programMine reads the user's reports to program p; nil without any.
func (s *server) programMine(ctx context.Context, p *store.ProgramDetail) (*programMine, error) {
	if s.ReadOnly {
		return nil, nil
	}
	rs, err := s.Store.MyReports(ctx, store.ReportFilter{ProgramID: p.ID})
	if err != nil || len(rs) == 0 {
		return nil, err
	}
	published, err := s.Store.PublishedTimes(ctx, []int64{p.ID})
	if err != nil {
		return nil, err
	}
	now := s.Store.Now()
	_, quiet := s.quietAfter(ctx)
	a := reports.Analyse(rs, reports.Range{To: now.Add(time.Second)}, s.shownZone(), published, now)
	m := &programMine{Open: a.Open, Decided: a.Decided, Acceptance: a.Acceptance,
		AllURL: "/reports?" + url.Values{"program": {strconv.FormatInt(p.ID, 10)}, "status": {"all"}}.Encode()}
	if len(a.Programs) > 0 {
		m.ProgramRow = a.Programs[0]
	}
	y, t := m.Yours, m.Theirs
	triage, triageTitle := t.Triage, "Average from submission to triage"
	if triage == nil && t.Validation != nil {
		triage, triageTitle = t.Validation, "Average from submission to triage; the program publishes its time to validate"
	}
	for _, c := range []mineTime{
		{"First response", "Average from submission to the program's first response", y.FirstResponse, t.FirstResponse},
		{"Triage", triageTitle, y.Triage, triage},
		{"Bounty", "Average from submission to the first bounty", y.Bounty, t.Bounty},
		{"Resolution", "Average from submission to resolution", y.Resolution, t.Resolution},
	} {
		if c.Yours.N > 0 || (c.Theirs != nil && (c.Label != "Bounty" || p.OffersBounty)) {
			m.Times = append(m.Times, c)
		}
	}
	sortReports(rs, "submitted", false)
	for _, rep := range rs[:min(mineShown, len(rs))] {
		m.Recent = append(m.Recent, s.reportRow(rep, now, quiet))
	}
	return m, nil
}
