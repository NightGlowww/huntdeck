package hackerone

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
)

func (f *fakeAPI) reportRoutes() {
	f.override["/hackers/me/reports"] = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page[number]") == "2" {
			f.fixture(w, "reports_page2.json")
			return
		}
		f.fixture(w, "reports_page1.json")
	}
	f.override["/hackers/reports/5001"] = func(w http.ResponseWriter, _ *http.Request) { f.fixture(w, "report_5001.json") }
	f.override["/hackers/reports/5002"] = func(w http.ResponseWriter, _ *http.Request) { f.fixture(w, "report_5002.json") }
	f.override["/hackers/reports/5000"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data": {"id": "5000", "relationships": {"activities": {"data": []}}}}`))
	}
	f.override["/hackers/payments/earnings"] = func(w http.ResponseWriter, _ *http.Request) { f.fixture(w, "earnings.json") }
}

func fetchAll(t *testing.T, c *Client, known map[string]string, maxDetails int) ([]platform.Report, bool, error) {
	t.Helper()
	var got []platform.Report
	complete, err := c.FetchReports(context.Background(), known, maxDetails, func(_ context.Context, rs []platform.Report) error {
		got = append(got, rs...)
		return nil
	})
	return got, complete, err
}

// Every page is listed, newest first; details give the timeline, the
// severity submitted, comments and who spoke last; earnings give money in
// the currency awarded. Nothing of the write-up or comments is kept.
func TestFetchReports(t *testing.T) {
	f := newFakeAPI(t)
	f.reportRoutes()
	c := f.client(t, "user", "token")
	got, complete, err := fetchAll(t, c, nil, 10)
	if err != nil || !complete {
		t.Fatalf("complete=%v err=%v", complete, err)
	}
	if len(got) != 3 || got[0].Key != "5002" || got[1].Key != "5001" || got[2].Key != "5000" {
		t.Fatalf("reports %v, want 5002, 5001, 5000", keysOf(got))
	}
	xss, redirect, dup := got[1], got[0], got[2]
	if xss.State != platform.ReportResolved || xss.ProgramHandle != "acme" || xss.URL != "https://hackerone.com/reports/5001" {
		t.Errorf("xss: %+v", xss)
	}
	if xss.Severity != platform.SeverityHigh || xss.SubmittedSeverity != platform.SeverityMedium || !xss.SeverityChanged {
		t.Errorf("xss severity %q submitted %q changed %v, want high from medium", xss.Severity, xss.SubmittedSeverity, xss.SeverityChanged)
	}
	if xss.TriagedAt == nil || xss.TriagedAt.Format("01-02 15") != "02-02 09" {
		t.Errorf("triaged %v, want the first pending-program-review", xss.TriagedAt)
	}
	if xss.Comments != 2 || xss.WaitingOn != "" || !xss.Detailed {
		t.Errorf("xss comments %d waiting %q detailed %v", xss.Comments, xss.WaitingOn, xss.Detailed)
	}
	if !xss.PaymentsKnown || len(xss.Payments) != 3 {
		t.Fatalf("payments %+v", xss.Payments)
	}
	if p := xss.Payments[0]; p.Kind != "bounty" || p.Amount != 920 || p.Currency != "EUR" {
		t.Errorf("bounty %+v, want 920 EUR as awarded", p)
	}
	if p := xss.Payments[2]; p.Kind != "retest" || p.Amount != 50 || p.Currency != "USD" {
		t.Errorf("retest %+v", p)
	}
	if redirect.State != platform.ReportNew || redirect.WaitingOn != "you" || redirect.SubmittedSeverity != platform.SeverityLow || redirect.SeverityChanged {
		t.Errorf("redirect: %+v", redirect)
	}
	if dup.State != platform.ReportDuplicate || dup.Severity != "" || len(dup.Payments) != 0 || !dup.PaymentsKnown {
		t.Errorf("duplicate: %+v", dup)
	}
	for _, r := range got {
		b, _ := json.Marshal(r)
		if strings.Contains(string(b), "NEVER") || strings.Contains(string(b), "hunter") {
			t.Errorf("report %s keeps text it mustn't: %s", r.Key, b)
		}
	}

	// A second run reads details only for reports whose marker moved, and
	// at most maxDetails of them.
	known := map[string]string{}
	for _, r := range got {
		known[r.Key] = r.Marker
	}
	known["5000"] = "old"
	f.hits = map[string]int{}
	again, _, err := fetchAll(t, c, known, 10)
	if err != nil || len(again) != 3 {
		t.Fatal(err)
	}
	if f.hits["/hackers/reports/5001"]+f.hits["/hackers/reports/5002"] != 0 || f.hits["/hackers/reports/5000"] != 1 {
		t.Errorf("detail requests %v, want only 5000's", f.hits)
	}
	f.hits = map[string]int{}
	capped, _, _ := fetchAll(t, c, nil, 1)
	if f.hits["/hackers/reports/5002"] != 1 || f.hits["/hackers/reports/5001"]+f.hits["/hackers/reports/5000"] != 0 || capped[1].Detailed {
		t.Errorf("capped run read %v, want the newest only", f.hits)
	}
}

// A failed list page fails the run (not complete); a failed detail or
// earnings read still saves the listing and says why.
func TestFetchReportsFailures(t *testing.T) {
	f := newFakeAPI(t)
	f.reportRoutes()
	f.override["/hackers/me/reports"] = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page[number]") == "2" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.fixture(w, "reports_page1.json")
	}
	c := f.client(t, "user", "token")
	if got, complete, err := fetchAll(t, c, nil, 10); err == nil || complete || len(got) != 0 {
		t.Errorf("list failure: %d reports complete=%v err=%v", len(got), complete, err)
	}

	f2 := newFakeAPI(t)
	f2.reportRoutes()
	f2.override["/hackers/reports/5002"] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }
	f2.override["/hackers/payments/earnings"] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }
	got, complete, err := fetchAll(t, f2.client(t, "user", "token"), nil, 10)
	if err == nil || !complete || len(got) != 3 {
		t.Fatalf("detail failure: %d reports complete=%v err=%v", len(got), complete, err)
	}
	for _, r := range got {
		if r.Detailed || r.PaymentsKnown {
			t.Errorf("report %s detailed=%v payments=%v after the failures", r.Key, r.Detailed, r.PaymentsKnown)
		}
	}
}

func keysOf(rs []platform.Report) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Key)
	}
	return out
}
