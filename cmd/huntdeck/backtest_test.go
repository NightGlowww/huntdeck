package main

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/db"
)

// The backtest harness has to be right before the data arrives, or its first
// real answer will be wrong in a way nobody checked. This seeds two days of
// scores and a bounty paid after each, then reads the capture share back.
func TestBacktestCapturesWhatWasPaid(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(dir, "huntdeck.db"))
	if err != nil {
		t.Fatal(err)
	}
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Migrate(ctx, migs, dir, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	const insertProgram = `INSERT INTO programs (platform, handle, name, url, state, offers_bounty, private, first_seen_at, last_seen_at)
		VALUES ('fake', ?, ?, 'https://example.com/x', 'open', 1, 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`
	const insertScore = `INSERT INTO hunt_scores (program_id, day, score, fresh, competition, payout, response)
		VALUES (?, ?, ?, 10, 10, 10, 10)`
	const insertBounty = `INSERT INTO accepted_reports (program_id, report_id, accepted_at, bounty, priority, first_seen_at, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?)`

	for i, p := range []struct {
		handle string
		payout float64
	}{{"alpha", 500}, {"beta", 100}} {
		if _, err := d.W.ExecContext(ctx, insertProgram, p.handle, p.handle); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := d.R.QueryRowContext(ctx, `SELECT id FROM programs WHERE handle = ?`, p.handle).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for _, day := range []string{"2026-11-01", "2026-11-02"} {
			score := 40.0
			if i == 0 {
				score = 90
			}
			if _, err := d.W.ExecContext(ctx, insertScore, id, day, score); err != nil {
				t.Fatal(err)
			}
			if _, err := d.W.ExecContext(ctx, insertBounty, id, p.handle+day, day+"T12:00:00Z", p.payout, day+"T12:00:00Z", day+"T12:00:00Z"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if code := backtestCmd([]string{"-data", dir, "-top", "1"}, &out, &errOut); code != 0 {
		t.Fatalf("backtest failed: %s", errOut.String())
	}
	body := out.String()
	if !strings.Contains(body, "cuts available (days with HuntScore): 2") {
		t.Errorf("cut count: %s", body)
	}
	// The better-scored program earned 500 of the 600 paid after each cut.
	if !strings.Contains(body, "(83.3%)") {
		t.Errorf("capture share: %s", body)
	}
}
