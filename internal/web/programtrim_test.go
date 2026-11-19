package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// A program page leaves out what has no data and what repeats.
func TestProgramPageTrims(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	tests := []struct {
		path       string
		want, lack []string
	}{
		// A program that doesn't pay: six headline numbers, no money.
		{"/programs/fake/evil", []string{`class="panel kpis six"`, `<span class="v">None</span><span class="h">disclosure only</span>`},
			[]string{">Total paid<", ">Paid · 90 days<", ">Paid per report<", ">Rewarded reports<", ">You earned<"}},
		// A paying one: all eight. Its top bounty is the table's, so isn't
		// repeated.
		{"/programs/fake/acme", []string{`class="panel kpis"`, ">Total paid<", ">Paid · 90 days<"},
			[]string{"<dt title=\"More than its bounty table pays\">Top bounty</dt>"}},
		// Before you test is on Overview and Scope, not beside the policy.
		{"/programs/fake/acme?tab=policy", []string{`<div class="polgrid">`, "<h2>Bounty table</h2>"}, []string{"Before you test"}},
	}
	for _, tt := range tests {
		_, body := e.do(c, http.MethodGet, tt.path, nil, nil)
		for _, w := range tt.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s lacks %q", tt.path, w)
			}
		}
		for _, l := range tt.lack {
			if strings.Contains(body, l) {
				t.Errorf("%s has %q", tt.path, l)
			}
		}
	}
}

func TestTopBeyondTable(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	table := []platform.RewardRow{{Critical: platform.RewardRange{Min: 1000, Max: 2500}}}
	tests := []struct {
		name    string
		lo, hi  *float64
		rewards []platform.RewardRow
		want    bool
	}{
		{"the table's top", f(1000), f(2500), table, false},
		{"past the table", f(5000), f(5000), table, true},
		{"no table", f(5000), nil, nil, true},
		{"no top", nil, nil, table, false},
	}
	for _, tt := range tests {
		s := &store.ProgramStats{ProgramStats: platform.ProgramStats{TopBountyLow: tt.lo, TopBountyHigh: tt.hi, Rewards: tt.rewards}}
		if got := topBeyondTable(s); got != tt.want {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
}

// Announcements get their own segment, so they don't push scope changes
// off the Overview.
func TestProgramFeed(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	ctx := context.Background()
	p, err := e.store.Program(ctx, "fake", "acme")
	if err != nil {
		t.Fatal(err)
	}
	err = e.store.Write(ctx, func(tx *store.Tx) error {
		for i, title := range []string{"Holiday freeze", "New assets soon"} {
			a := platform.Announcement{ID: title, Title: title, Body: "x", PublishedAt: time.Now().Add(time.Duration(i) * time.Minute)}
			if _, err := tx.AddAnnouncement(ctx, p.ID, 0, a, true); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, body := e.do(c, http.MethodGet, "/programs/fake/acme", nil, nil)
	if !strings.Contains(body, `<a class="on" aria-current="true" href="?feed=program">Scope &amp; program`) || strings.Contains(body, "Holiday freeze") {
		t.Errorf("overview feed doesn't start on scope and program changes")
	}
	_, body = e.do(c, http.MethodGet, "/programs/fake/acme?feed=announcements", nil, nil)
	if !strings.Contains(body, "Holiday freeze") || strings.Contains(body, `<span class="tag add">`) {
		t.Errorf("announcements segment")
	}
	_, body = e.do(c, http.MethodGet, "/programs/fake/acme?tab=changes", nil, nil)
	if !strings.Contains(body, `<a class="on" aria-current="true" href="?tab=changes&amp;feed=all">All`) || !strings.Contains(body, "Holiday freeze") ||
		!strings.Contains(body, `<span class="tag add">`) {
		t.Errorf("changes tab doesn't start on all changes")
	}
}

// The Scope changes segment keeps only the entries added, removed or
// edited: the changes a hunter opens the tab for.
func TestProgramFeedScopeSegment(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	ctx := context.Background()
	p, err := e.store.Program(ctx, "fake", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.Write(ctx, func(tx *store.Tx) error {
		a := platform.Announcement{ID: "freeze", Title: "Holiday freeze", Body: "x", PublishedAt: time.Now()}
		_, err := tx.AddAnnouncement(ctx, p.ID, 0, a, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, body := e.do(c, http.MethodGet, "/programs/fake/acme?tab=changes", nil, nil)
	if !strings.Contains(body, "feed=scope") {
		t.Fatalf("the changes tab offers no scope segment")
	}
	_, body = e.do(c, http.MethodGet, "/programs/fake/acme?tab=changes&feed=scope", nil, nil)
	if !strings.Contains(body, `<span class="tag add">`) {
		t.Errorf("the scope segment shows no scope changes")
	}
	if strings.Contains(body, "Holiday freeze") {
		t.Errorf("the scope segment shows an announcement")
	}
}
