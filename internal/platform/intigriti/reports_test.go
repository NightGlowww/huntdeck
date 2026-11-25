package intigriti

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
)

func fetchReports(t *testing.T, c *Client, known map[string]string, maxDetails int) ([]platform.Report, bool, error) {
	t.Helper()
	var got []platform.Report
	complete, err := c.FetchReports(context.Background(), known, maxDetails, func(_ context.Context, rs []platform.Report) error {
		got = append(got, rs...)
		return nil
	})
	return got, complete, err
}

// Reports come from the web app, newest first, deleted ones left out; the
// codes mean what Intigriti's app says they do; payouts carry the date the
// API says they were paid. No write-up, message or name is kept.
func TestFetchReports(t *testing.T) {
	f := &fakeWeb{}
	c := webClient(t, f, func(c *Client) error { return f.giveSSO(c) })
	got, complete, err := fetchReports(t, c, nil, 10)
	if err != nil || !complete {
		t.Fatalf("complete=%v err=%v", complete, err)
	}
	if len(got) != 3 || got[0].Key != "ACME-BBBB2222" || got[1].Key != "ACME-AAAA1111" || got[2].Key != "GLOB-CCCC3333" {
		t.Fatalf("got %d reports, want 3 newest first without the deleted one", len(got))
	}
	ssrf, rate, dup := got[1], got[0], got[2]
	if ssrf.State != platform.ReportResolved || ssrf.RawState != "Archived: Resolved" || ssrf.ProgramHandle != acmeID ||
		ssrf.URL != "https://app.intigriti.com/researcher/submissions/"+acmeID+"/ACME-AAAA1111" {
		t.Errorf("ssrf: %+v", ssrf)
	}
	if ssrf.Severity != platform.SeverityHigh || ssrf.SubmittedSeverity != platform.SeverityMedium || !ssrf.SeverityChanged {
		t.Errorf("ssrf severity %q from %q changed %v", ssrf.Severity, ssrf.SubmittedSeverity, ssrf.SeverityChanged)
	}
	if ssrf.FirstResponseAt == nil || ssrf.FirstResponseAt.Unix() != 1790003600 || ssrf.TriagedAt.Unix() != 1790003700 || ssrf.ClosedAt.Unix() != 1791000000 {
		t.Errorf("ssrf times: response %v triaged %v closed %v", ssrf.FirstResponseAt, ssrf.TriagedAt, ssrf.ClosedAt)
	}
	if ssrf.Comments != 2 || ssrf.WaitingOn != "" {
		t.Errorf("ssrf comments %d waiting %q", ssrf.Comments, ssrf.WaitingOn)
	}
	if len(ssrf.Payments) != 2 || ssrf.Payments[0].Kind != "bounty" || ssrf.Payments[0].Currency != "EUR" || ssrf.Payments[0].PaidAt == nil ||
		ssrf.Payments[0].PaidAt.Unix() != 1790800000 || ssrf.Payments[1].Kind != "bonus" || ssrf.Payments[1].PaidAt != nil {
		t.Errorf("ssrf payments %+v", ssrf.Payments)
	}
	if rate.State != platform.ReportNew || rate.Severity != "" || rate.SeverityLabel != "Undecided" || rate.SubmittedSeverity != platform.SeverityHigh ||
		rate.WaitingOn != "you" || !rate.Unread {
		t.Errorf("rate limit: %+v", rate)
	}
	if dup.State != platform.ReportDuplicate || dup.RawState != "Closed: Duplicate" || dup.ProgramName != "Not in HuntDeck" {
		t.Errorf("duplicate: %+v", dup)
	}
	for _, r := range got {
		b, _ := json.Marshal(r)
		if s := string(b); strings.Contains(s, "NEVER") || strings.Contains(s, "hunter") || strings.Contains(s, "triager") || strings.Contains(s, "192.0.2.1") {
			t.Errorf("%s keeps text it mustn't: %s", r.Key, s)
		}
	}

	// Unchanged reports aren't read again.
	known := map[string]string{}
	for _, r := range got {
		known[r.Key] = r.Marker
	}
	f.mu.Lock()
	f.hits = map[string]int{}
	f.mu.Unlock()
	if _, _, err := fetchReports(t, c, known, 10); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, n := range f.hits {
		if strings.Contains(k, "/submissions/") || strings.Contains(k, "payouts") {
			t.Errorf("unchanged run asked %s %d times", k, n)
		}
	}
}

// Without the web login there are no reports to read.
func TestFetchReportsNeedsLogin(t *testing.T) {
	c, err := New(Options{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	if c.ReportsConfigured() {
		t.Error("configured without a web login")
	}
	if _, err := c.FetchReports(context.Background(), nil, 10, nil); err == nil {
		t.Error("no error without a web login")
	}
}
