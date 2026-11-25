package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

func reportAt(day int) time.Time { return time.Date(2026, 3, day, 10, 0, 0, 0, time.UTC) }

// A sync stores reports, keeps what the user set by hand, reads details
// only when they were read whole, and reports what changed: a new report
// alone, then state, severity, bounties and the program waiting.
func TestSaveReports(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t, nil)
	exec(t, st, `INSERT INTO programs (id, platform, handle, name, url, private, state, offers_bounty, raw, first_seen_at, last_seen_at)
		VALUES (7, 'bugcrowd', 'Acme-BB', 'Acme', 'https://example.com/acme', 1, 'open', 1, '{}', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`)
	first := platform.Report{Key: "r1", ProgramHandle: "acme-bb", ProgramName: "Acme", Title: "XSS on example.com", URL: "https://example.com/r1",
		State: platform.ReportNew, RawState: "nue", SubmittedAt: reportAt(1), Marker: "m1", Comments: 1}
	ch, err := st.SaveReports(ctx, "bugcrowd", []platform.Report{first})
	if err != nil {
		t.Fatal(err)
	}
	if len(ch) != 1 || ch[0].Kind != ReportChangeNew {
		t.Fatalf("first save: %+v, want one new", ch)
	}
	if m, _ := st.ReportMarkers(ctx, "bugcrowd"); len(m) != 0 {
		t.Errorf("markers before details were read: %v", m)
	}
	rs, err := st.MyReports(ctx, ReportFilter{})
	if err != nil || len(rs) != 1 {
		t.Fatalf("reports: %v %v", rs, err)
	}
	r := rs[0]
	if r.ProgramID == nil || *r.ProgramID != 7 || r.ProgramHandle != "Acme-BB" || !r.ProgramPrivate {
		t.Errorf("program not matched by handle regardless of case: %+v", r)
	}

	// The user corrects the title; the details are read with a bounty that
	// was there all along and the severity triage gave it (not news: the
	// first details are the report's past).
	if err := st.UpdateReport(ctx, r.ID, ReportInput{Title: "My title", Set: []string{"title"}}); err != nil {
		t.Fatal(err)
	}
	second := first
	second.State, second.RawState, second.Severity, second.SubmittedSeverity, second.SeverityChanged = platform.ReportAccepted, "unresolved", platform.SeverityHigh, platform.SeverityMedium, true
	second.Detailed, second.PaymentsKnown, second.Marker = true, true, "m2"
	second.Payments = []platform.ReportPayment{{Kind: "bounty", Amount: 500, Currency: "usd", AwardedAt: reportAt(5)}}
	second.Events = []platform.ReportEvent{{At: reportAt(2), Kind: platform.EventState, From: "nue", To: "triaged"}}
	if ch, err = st.SaveReports(ctx, "bugcrowd", []platform.Report{second}); err != nil {
		t.Fatal(err)
	}
	if kinds := changeKinds(ch); kinds != "state" {
		t.Errorf("second save changes %q, want state (the bounty and severity were read for the first time)", kinds)
	}
	r, _ = st.MyReportByID(ctx, r.ID)
	if r.Title != "My title" || r.State != platform.ReportAccepted || r.SubmittedSeverity != platform.SeverityMedium || !r.SeverityChanged {
		t.Errorf("after details: %+v", r)
	}
	if len(r.Payments) != 1 || r.Payments[0].Currency != "USD" || r.Payments[0].Amount != 500 {
		t.Errorf("payments: %+v", r.Payments)
	}
	if ev, _ := st.MyReportEvents(ctx, r.ID); len(ev) != 1 {
		t.Errorf("events: %+v", ev)
	}
	if m, _ := st.ReportMarkers(ctx, "bugcrowd"); m["r1"] != "m2" {
		t.Errorf("marker %v, want m2", m)
	}

	// A listing-only save keeps what only details give; a new bonus and
	// the program asking for something are news.
	third := first
	third.State, third.Severity, third.Marker, third.Comments = platform.ReportAccepted, platform.SeverityHigh, "m3", 0
	if ch, err = st.SaveReports(ctx, "bugcrowd", []platform.Report{third}); err != nil {
		t.Fatal(err)
	}
	if len(ch) != 0 {
		t.Errorf("listing-only save changes %+v, want none", ch)
	}
	r, _ = st.MyReportByID(ctx, r.ID)
	if r.SubmittedSeverity != platform.SeverityMedium || !r.SeverityChanged || r.Comments != 1 || len(r.Payments) != 1 {
		t.Errorf("listing-only save lost detail fields: %+v", r)
	}
	if m, _ := st.ReportMarkers(ctx, "bugcrowd"); m["r1"] != "m2" {
		t.Errorf("marker moved without details: %v", m)
	}
	fourth := second
	fourth.Marker, fourth.WaitingOn = "m4", "you"
	fourth.Payments = append(fourth.Payments, platform.ReportPayment{Kind: "bonus", Amount: 50, Currency: "USD", AwardedAt: reportAt(9)})
	if ch, err = st.SaveReports(ctx, "bugcrowd", []platform.Report{fourth}); err != nil {
		t.Fatal(err)
	}
	if kinds := changeKinds(ch); kinds != "bounty,waiting" {
		t.Errorf("fourth save changes %q, want bounty,waiting", kinds)
	}

	// A complete listing without it marks it gone; seen again, it's back.
	if n, err := st.MarkReportsGone(ctx, "bugcrowd", []string{"other"}); err != nil || n != 1 {
		t.Errorf("gone: %d %v", n, err)
	}
	if n, _ := st.MarkReportsGone(ctx, "bugcrowd", nil); n != 0 {
		t.Errorf("an empty listing marked %d gone", n)
	}
	if _, err := st.SaveReports(ctx, "bugcrowd", []platform.Report{third}); err != nil {
		t.Fatal(err)
	}
	if r, _ = st.MyReportByID(ctx, r.ID); r.GoneAt != nil {
		t.Errorf("seen again but still gone")
	}
}

func changeKinds(ch []ReportChange) string {
	var s string
	for i, c := range ch {
		if i > 0 {
			s += ","
		}
		s += c.Kind
	}
	return s
}

// Reports added by hand: stored with their payments, edited, deleted;
// synced ones can't be deleted.
func TestManualReports(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t, nil)
	id, err := st.AddManualReport(ctx, ReportInput{Platform: "independent", ProgramName: "Example Corp", Title: "IDOR", State: platform.ReportResolved,
		Severity: platform.SeverityHigh, SubmittedAt: reportAt(3), Payments: []MyPayment{{Kind: "retest", Amount: 100, Currency: "eur", AwardedAt: reportAt(10)},
			{Kind: "bounty", Amount: 1000, Currency: "eur", AwardedAt: reportAt(20)}}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := st.MyReportByID(ctx, id)
	if err != nil || r.Source != ReportByHand || r.ProgramName != "Example Corp" || len(r.Payments) != 2 || r.Payments[1].Currency != "EUR" {
		t.Fatalf("manual report: %+v %v", r, err)
	}
	// Its first bounty is the first bounty paid (not the retest before it).
	if r.FirstBountyAt == nil || !r.FirstBountyAt.Equal(reportAt(20)) {
		t.Errorf("first bounty of a report added by hand = %v, want %v", r.FirstBountyAt, reportAt(20))
	}
	if err := st.UpdateReport(ctx, id, ReportInput{State: platform.ReportDuplicate, Set: []string{"state", "payments"}}); err != nil {
		t.Fatal(err)
	}
	if r, _ = st.MyReportByID(ctx, id); r.State != platform.ReportDuplicate || len(r.Payments) != 0 || len(r.Edits) != 0 {
		t.Errorf("after edit: %+v", r)
	}
	if err := st.DeleteManualReport(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MyReportByID(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted report: %v", err)
	}
	if _, err := st.SaveReports(ctx, "hackerone", []platform.Report{{Key: "9", State: platform.ReportNew, SubmittedAt: reportAt(1)}}); err != nil {
		t.Fatal(err)
	}
	rs, _ := st.MyReports(ctx, ReportFilter{Platform: "hackerone"})
	if err := st.DeleteManualReport(ctx, rs[0].ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting a synced report: %v", err)
	}
}

// Every editable field has its update.
func TestReportFieldUpdates(t *testing.T) {
	for _, f := range EditableReportFields {
		if reportFieldUpdates[f] == "" {
			t.Errorf("no update for %s", f)
		}
	}
	if len(reportFieldUpdates) != len(EditableReportFields) {
		t.Errorf("%d updates for %d fields", len(reportFieldUpdates), len(EditableReportFields))
	}
}

// News on reports is kept for the notifier once the platform's reports
// have been listed before, never for a muted report, and let go once
// queued.
func TestReportNotices(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t, nil)
	r1 := platform.Report{Key: "r1", ProgramName: "Acme", Title: "XSS", State: platform.ReportNew, SubmittedAt: reportAt(1)}
	r2 := platform.Report{Key: "r2", ProgramName: "Acme", Title: "IDOR", State: platform.ReportNew, SubmittedAt: reportAt(2)}
	if _, err := st.SaveReports(ctx, "hackerone", []platform.Report{r1, r2}); err != nil {
		t.Fatal(err)
	}
	if ns, err := st.ReportNotices(ctx); err != nil || len(ns) != 0 {
		t.Fatalf("the first listing made notices: %+v %v", ns, err)
	}
	if err := st.SetReportsSynced(ctx, "hackerone"); err != nil {
		t.Fatal(err)
	}
	rs, _ := st.MyReports(ctx, ReportFilter{})
	for _, r := range rs {
		if r.Key == "r2" {
			if err := st.MuteReport(ctx, r.ID, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	r1.State, r2.State = platform.ReportTriaged, platform.ReportTriaged
	r3 := platform.Report{Key: "r3", ProgramName: "Acme", Title: "SSRF", State: platform.ReportNew, SubmittedAt: reportAt(3)}
	if _, err := st.SaveReports(ctx, "hackerone", []platform.Report{r1, r2, r3}); err != nil {
		t.Fatal(err)
	}
	ns, err := st.ReportNotices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 2 || ns[0].Kind != ReportChangeState || ns[0].From != "new" || ns[0].To != "triaged" || ns[0].Report.Title != "XSS" ||
		ns[1].Kind != ReportChangeNew || ns[1].Report.Title != "SSRF" {
		t.Fatalf("notices: %+v", ns)
	}
	if err := st.QueueReportNotices(ctx, []int64{ns[0].ID, ns[1].ID}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if ns, _ := st.ReportNotices(ctx); len(ns) != 0 {
		t.Errorf("queued notices still waiting: %+v", ns)
	}
}
