package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/huntqueue"
	"github.com/mdpsec/huntdeck/internal/jev"
	"github.com/mdpsec/huntdeck/internal/overrides"
	"github.com/mdpsec/huntdeck/internal/scorecheck"
	"github.com/mdpsec/huntdeck/internal/store"
)

// auditTextsCmd asks the decision model whether the sentences HuntDeck writes
// about a target are supported by the numbers behind them.
//
// This is the audit that can actually fail: the claims come from the renderer
// and the evidence from the stored inputs, two paths that are meant to agree and
// have not. Auditing a page against the same value it was generated from found
// nothing in 44 claims; auditing the queue's own sentences found a top bounty
// rendered as "$3000.0k" within the first minute.
func auditTextsCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("audit-texts", flag.ContinueOnError)
	fs.SetOutput(stderr)
	data := fs.String("data", "", "data directory (default $HUNTDECK_DATA_DIR, else ./data)")
	limit := fs.Int("limit", 10, "how many targets to audit")
	asJSON := fs.Bool("json", false, "print the rows as JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	dir := *data
	if dir == "" {
		dir = os.Getenv("HUNTDECK_DATA_DIR")
	}
	if dir == "" {
		dir = "data"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	d, err := db.Open(ctx, dir+"/huntdeck.db")
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck audit-texts: %v\n", err)
		return 1
	}
	defer func() { _ = d.Close() }()
	st := store.New(d, time.Now)

	cfg, err := overrides.New(st, dir, config.OSSource).Config(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck audit-texts: %v\n", err)
		return 1
	}
	if cfg.JevKey.Reveal() == "" {
		fmt.Fprintln(stderr, "huntdeck audit-texts: no decision model configured: set HUNTDECK_JEV_KEY (Settings > Accounts > Decision model)")
		return 1
	}

	rows, err := st.QueueCandidates(ctx, *limit)
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck audit-texts: %v\n", err)
		return 1
	}
	if len(rows) > *limit {
		rows = rows[:*limit]
	}
	client := &jev.Client{BaseURL: cfg.JevURL, Key: cfg.JevKey.Reveal(), Model: cfg.JevModel, Proxy: cfg.JevProxy, Provider: cfg.JevProvider, AccountID: cfg.JevAccountID, WorkspaceID: cfg.JevWorkspaceID, Timeout: 15 * time.Second}

	type finding struct {
		Program  string
		Target   string
		Sentence string
		Stored   map[string]string
		P        float64
		Call     string
	}
	var found []finding
	suspects := 0
	for _, in := range rows {
		res := huntqueue.Score(huntqueue.Inputs{
			ProgramValue: in.ProgramValue, PayoutUSD: in.PayoutUSD, MedianDays: in.SpeedDays,
			Disclosed365: in.Disclosed365, InScope: in.InScope, AddedAt: in.AddedAt, Now: time.Now(),
			Flips: in.Flips, NeedsCreds: in.NeedsCreds,
		})
		evidence := map[string]string{
			"payout_usd":     fmt.Sprintf("%.2f", in.PayoutUSD),
			"speed_days":     fmt.Sprintf("%.2f", in.SpeedDays),
			"in_scope":       fmt.Sprintf("%d", in.InScope),
			"program_value":  fmt.Sprintf("%.2f", in.ProgramValue),
			"disclosed_365d": fmt.Sprintf("%d", in.Disclosed365),
		}
		for _, sentence := range huntqueue.English(res.Reasons) {
			p, _, err := client.Probability(ctx, evidence,
				"Does this stored data support this sentence about the target: "+sentence)
			if err != nil {
				fmt.Fprintf(stderr, "huntdeck audit-texts: %v\n", err)
				return 1
			}
			call := scorecheck.Classify(p)
			if call == scorecheck.CallUnsupported {
				suspects++
				found = append(found, finding{
					Program: in.Platform + "/" + in.Handle, Target: in.Kind + " " + in.Value,
					Sentence: sentence, Stored: evidence, P: p, Call: call,
				})
			}
		}
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(found)
	} else {
		fmt.Fprintf(stdout, "audited %d target(s), %d sentence(s) read as unsupported\n", len(rows), suspects)
		for _, f := range found {
			fmt.Fprintf(stdout, "  %.2f  %s\n        %s\n        stored: %v\n", f.P, f.Sentence, f.Target, f.Stored)
		}
	}
	if suspects > 0 {
		return 1
	}
	return 0
}
