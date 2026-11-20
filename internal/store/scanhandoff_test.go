package store

import (
	"context"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// ScanCandidates had no test, and its SELECT decides what the handoff may send:
// a column out of place would empty the scan page silently, because the page
// ignores the error and shows "nothing waiting". This pins the three things it
// has to get right - only in-scope entries of open programs, never one already
// handed off, and the program's handle, which is what makes a task attributable.
func TestScanCandidatesCarryTheirProgram(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	if err := s.Write(ctx, func(tx *Tx) error {
		for _, p := range []struct {
			handle, state string
			ignored       bool
			scopes        []struct {
				ident   string
				inScope bool
			}
		}{
			{"acme", "open", false, []struct {
				ident   string
				inScope bool
			}{{"a.acme.example", true}, {"b.acme.example", true}, {"out.acme.example", false}}},
			{"other", "open", false, []struct {
				ident   string
				inScope bool
			}{{"c.other.example", true}}},
			{"quiet", "paused", false, []struct {
				ident   string
				inScope bool
			}{{"d.quiet.example", true}}},
			{"ignored", "open", true, []struct {
				ident   string
				inScope bool
			}{{"e.ignored.example", true}}},
		} {
			id, err := tx.UpsertProgram(ctx, "fake", platform.Program{Handle: p.handle, Name: p.handle,
				State: p.state, OffersBounty: true})
			if err != nil {
				return err
			}
			for i, e := range p.scopes {
				if _, err := tx.InsertEntry(ctx, id, e.ident, e.ident, platform.ScopeEntry{
					InScope: e.inScope, AssetType: "domain", Identifier: e.ident}, i == 0); err != nil {
					return err
				}
			}
			if err := tx.RefreshScopeCounts(ctx, id); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Ignoring is a store call of its own, not a field on the program.
	var ignoredID int64
	if err := s.db.R.QueryRowContext(ctx, `SELECT id FROM programs WHERE handle = 'ignored'`).Scan(&ignoredID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetIgnored(ctx, ignoredID, true); err != nil {
		t.Fatal(err)
	}

	cands, err := s.ScanCandidates(ctx, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range cands {
		got[c.Target] = c.Handle
	}
	for _, want := range []string{"a.acme.example", "b.acme.example", "c.other.example"} {
		if _, ok := got[want]; !ok {
			t.Errorf("%s is not a candidate", want)
		}
	}
	if got["a.acme.example"] != "acme" {
		t.Errorf("a.acme.example says it belongs to %q", got["a.acme.example"])
	}
	for _, unwanted := range []string{"out.acme.example", "d.quiet.example", "e.ignored.example"} {
		if _, ok := got[unwanted]; ok {
			t.Errorf("%s should not be a candidate", unwanted)
		}
	}

	// A target already handed off does not come back.
	if err := s.MarkHandedOff(ctx, []ScanCandidate{{Target: "a.acme.example", Platform: "fake", Handle: "acme"}}, "task-1"); err != nil {
		t.Fatal(err)
	}
	cands, err = s.ScanCandidates(ctx, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cands {
		if c.Target == "a.acme.example" {
			t.Fatal("a handed-off target came back")
		}
	}
}

// And clearing the handoff makes it a candidate again.
func TestClearHandoffsBringsTargetsBack(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	if err := s.Write(ctx, func(tx *Tx) error {
		id, err := tx.UpsertProgram(ctx, "fake", platform.Program{Handle: "acme", Name: "acme", State: "open", OffersBounty: true})
		if err != nil {
			return err
		}
		if _, err := tx.InsertEntry(ctx, id, "a.acme.example", "a.acme.example",
			platform.ScopeEntry{InScope: true, AssetType: "domain", Identifier: "a.acme.example"}, true); err != nil {
			return err
		}
		return tx.RefreshScopeCounts(ctx, id)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkHandedOff(ctx, []ScanCandidate{{Target: "a.acme.example", Platform: "fake", Handle: "acme"}}, "task-9"); err != nil {
		t.Fatal(err)
	}
	if cands, _ := s.ScanCandidates(ctx, nil, 10); len(cands) != 0 {
		t.Fatalf("candidates before clearing: %v", cands)
	}
	n, err := s.ClearHandoffs(ctx, []string{"task-9"})
	if err != nil || n != 1 {
		t.Fatalf("cleared %d, %v", n, err)
	}
	if cands, _ := s.ScanCandidates(ctx, nil, 10); len(cands) != 1 {
		t.Fatalf("after clearing: %v", cands)
	}
}

// RecentHandoffs is the only way back from a task id to what went into it, so it
// has to group correctly, keep the newest first, and respect the limit.
func TestRecentHandoffsGroupByTask(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	batches := []struct {
		task    string
		targets []string
	}{
		{"task-old", []string{"a.example.com", "b.example.com"}},
		{"task-new", []string{"c.example.com"}},
	}
	for _, b := range batches {
		var items []ScanCandidate
		for _, tgt := range b.targets {
			items = append(items, ScanCandidate{Target: tgt, Platform: "intigriti", Handle: "h"})
		}
		if err := s.MarkHandedOff(ctx, items, b.task); err != nil {
			t.Fatal(err)
		}
		// created_at has second resolution; keep the order deterministic.
		time.Sleep(1100 * time.Millisecond)
	}

	byTask, order, err := s.RecentHandoffs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "task-new" {
		t.Fatalf("order = %v, want the newest first", order)
	}
	if got := len(byTask["task-old"].Targets); got != 2 {
		t.Fatalf("task-old carries %d targets", got)
	}
	if byTask["task-new"].Platform != "intigriti" {
		t.Fatalf("platform = %q", byTask["task-new"].Platform)
	}

	// The limit keeps the newest and does not leave half a batch behind.
	_, order, err = s.RecentHandoffs(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 1 || order[0] != "task-new" {
		t.Fatalf("limit 1 gave %v", order)
	}
}

// The handoff took the newest eight candidates, so while new scope kept arriving
// the older targets were never reached: one program was scanned repeatedly and
// the rest never. The backlog is read oldest first so it drains.
func TestScanBacklogDrainsOldestFirst(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	if err := s.Write(ctx, func(tx *Tx) error {
		id, err := tx.UpsertProgram(ctx, "fake", platform.Program{Handle: "acme", Name: "Acme", State: "open", OffersBounty: true})
		if err != nil {
			return err
		}
		for _, h := range []string{"old.example.com", "mid.example.com", "new.example.com"} {
			if _, err := tx.InsertEntry(ctx, id, h, h, platform.ScopeEntry{InScope: true, AssetType: "domain", Identifier: h}, true); err != nil {
				return err
			}
		}
		return tx.RefreshScopeCounts(ctx, id)
	}); err != nil {
		t.Fatal(err)
	}

	newest, err := s.ScanCandidates(ctx, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(newest) != 2 || newest[0].Target != "new.example.com" {
		t.Fatalf("newest first is broken: %+v", newest)
	}

	oldest, err := s.ScanBacklog(ctx, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(oldest) != 2 || oldest[0].Target != "old.example.com" {
		t.Fatalf("the backlog does not start with the oldest: %+v", oldest)
	}
	if oldest[0].Target == newest[0].Target {
		t.Fatal("both orders returned the same head")
	}
}
