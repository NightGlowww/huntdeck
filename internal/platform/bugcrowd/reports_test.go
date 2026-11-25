package bugcrowd

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

// Every page is read with the array-style offset; details come from the
// report's page and its activity feed; a feed on another host is refused.
// Nothing of the description, bug URL, comments or names is kept.
func TestFetchReports(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{Session: goodSession})
	got, complete, err := fetchReports(t, c, nil, 10)
	if !complete || len(got) != 3 {
		t.Fatalf("complete=%v reports=%d err=%v", complete, len(got), err)
	}
	if err == nil || !strings.Contains(err.Error(), "unexpected host") {
		t.Errorf("err %v, want the foreign feed refused", err)
	}
	ato, dup, hdr := got[0], got[1], got[2]
	if ato.Key != "aaaaaaaa-0000-4000-8000-000000000003" || ato.ProgramName != "Acme Bug Bounty" || ato.State != platform.ReportAccepted ||
		ato.URL != "https://bugcrowd.com/submissions/aaaaaaaa-0000-4000-8000-000000000003" {
		t.Errorf("ato: %+v", ato)
	}
	if ato.Severity != platform.SeverityHigh || ato.SeverityLabel != "P2" || ato.SubmittedSeverity != "" || !ato.SeverityChanged {
		t.Errorf("ato severity %q %q submitted %q changed %v, want P2, submitted unknown", ato.Severity, ato.SeverityLabel, ato.SubmittedSeverity, ato.SeverityChanged)
	}
	if ato.FirstResponseAt == nil || ato.FirstResponseAt.Hour() != 15 || ato.TriagedAt == nil || ato.TriagedAt.Day() != 11 || ato.WaitingOn != "" {
		t.Errorf("ato response %v triaged %v waiting %q", ato.FirstResponseAt, ato.TriagedAt, ato.WaitingOn)
	}
	if len(ato.Payments) != 1 || ato.Payments[0].Amount != 2500 || ato.Payments[0].Currency != "USD" || ato.Payments[0].Ref != "act-5" {
		t.Errorf("ato payments %+v", ato.Payments)
	}
	if dup.ClosedAt == nil || dup.ClosedAt.Day() != 11 {
		t.Errorf("dup closed %v, want the day it was marked a duplicate (no close date in the header)", dup.ClosedAt)
	}
	if dup.State != platform.ReportDuplicate || dup.RawState != "resolved (duplicate)" || dup.SubmittedSeverity != platform.SeverityLow ||
		dup.SeverityChanged || dup.ClosedAt == nil || len(dup.Payments) != 0 || !dup.PaymentsKnown {
		t.Errorf("dup: %+v", dup)
	}
	if hdr.Detailed || hdr.State != platform.ReportNew || hdr.WaitingOn != "you" {
		t.Errorf("header report (feed refused): %+v", hdr)
	}
	for _, r := range got {
		b, _ := json.Marshal(r)
		if s := string(b); strings.Contains(s, "NEVER") || strings.Contains(s, "hunter") {
			t.Errorf("%s keeps text it mustn't: %s", r.Key, s)
		}
	}
	if f.hit("/submissions.json") != 2 {
		t.Errorf("listing pages read %d, want 2", f.hit("/submissions.json"))
	}
}

// Without a login or session there is nothing to read.
func TestFetchReportsPublicOnly(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	if c.ReportsConfigured() {
		t.Error("configured in public-only mode")
	}
	if _, _, err := fetchReports(t, c, nil, 10); err == nil {
		t.Error("no error in public-only mode")
	}
}

func TestBCMoney(t *testing.T) {
	for _, tt := range []struct {
		in  any
		n   float64
		cur string
		ok  bool
	}{
		{"$1,234.50", 1234.5, "USD", true},
		{"€500", 500, "EUR", true},
		{"£75", 75, "GBP", true},
		{"CHF 300", 300, "CHF", true},
		{"¤ 10", 0, "", false},
		{"$0", 0, "", false},
		{nil, 0, "", false},
		{"500", 0, "", false},
	} {
		n, cur, ok := bcMoney(tt.in)
		if n != tt.n || cur != tt.cur || ok != tt.ok {
			t.Errorf("bcMoney(%v) = %v %q %v", tt.in, n, cur, ok)
		}
	}
}
