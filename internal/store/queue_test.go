package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/huntqueue"
	"github.com/mdpsec/huntdeck/internal/platform"
)

// BountyByProgram counts only accepted bounties inside the window, per program.
func TestBountyByProgramWindow(t *testing.T) {
	s := newTestStore(t, nil)
	ctx := context.Background()
	var a, b int64
	if err := s.Write(ctx, func(tx *Tx) error {
		var err error
		if a, err = tx.UpsertProgram(ctx, "fake", platform.Program{Handle: "acme", Name: "Acme", URL: "https://example.com/acme", State: "open"}); err != nil {
			return err
		}
		b, err = tx.UpsertProgram(ctx, "fake", platform.Program{Handle: "globex", Name: "Globex", URL: "https://example.com/globex", State: "open"})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	ins := func(pid int64, reportID, at string, bounty float64) {
		t.Helper()
		if _, err := s.db.W.ExecContext(ctx, `INSERT INTO accepted_reports
			(program_id, report_id, accepted_at, bounty, priority, first_seen_at, updated_at)
			VALUES (?, ?, ?, ?, 1, ?, ?)`, pid, reportID, at, bounty, at, at); err != nil {
			t.Fatal(err)
		}
	}
	ins(a, "r1", "2026-01-10T00:00:00.000Z", 500)
	ins(a, "r2", "2026-06-10T00:00:00.000Z", 1500) // outside the window
	ins(b, "r3", "2026-01-20T00:00:00.000Z", 200)

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	got, err := s.BountyByProgram(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if got[a] != 500 || got[b] != 200 {
		t.Fatalf("window sums: %+v, want acme=500 globex=200", got)
	}
}

// The queue says a target the scanner already has, instead of ranking it as if
// it were untouched: the same target used to look fresh after it was handed off.
func TestQueueMarksHandedOffTargets(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	if err := s.Write(ctx, func(tx *Tx) error {
		id, err := tx.UpsertProgram(ctx, "intigriti", platform.Program{Handle: "acme", Name: "Acme", State: "open"})
		if err != nil {
			return err
		}
		if _, err := tx.InsertEntry(ctx, id, "t.example.com", "t.example.com", platform.ScopeEntry{InScope: true, AssetType: "domain", Identifier: "t.example.com"}, true); err != nil {
			return err
		}
		if _, err := tx.tx.ExecContext(ctx, `INSERT INTO target_scores
			(program_id, kind, value, apex, bounty, added_at, score, reasons)
			VALUES (?, ?, ?, '', 500, ?, 60, '[]')`, id, "domain", "t.example.com",
			time.Now().UTC().Add(-10*24*time.Hour).Format(TimeFormat)); err != nil {
			return err
		}
		if _, err := tx.tx.ExecContext(ctx, `INSERT INTO scan_handoffs (target, platform, kind, task_id, created_at)
			VALUES (?, ?, ?, ?, ?)`, "t.example.com", "intigriti", "domain", "task-9", time.Now().UTC().Format(time.RFC3339)); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RefreshHuntQueue(ctx, s.Now()); err != nil {
		t.Fatal(err)
	}
	rows, _, err := s.HuntQueueTop(ctx, QueueFilter{}, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.Value != "t.example.com" {
			continue
		}
		found = true
		marked := false
		for _, rr := range huntqueue.English(row.Reasons) {
			if strings.Contains(rr, "already handed to the scanner") {
				marked = true
			}
		}
		if !marked {
			t.Fatalf("the handed-off target is not marked: %v", huntqueue.English(row.Reasons))
		}
	}
	if !found {
		t.Fatal("the target is not in the queue at all")
	}
}
