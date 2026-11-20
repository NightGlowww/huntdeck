package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/jev"
	"github.com/mdpsec/huntdeck/internal/overrides"
	"github.com/mdpsec/huntdeck/internal/store"
	"github.com/mdpsec/huntdeck/internal/triage"
)

// triageCmd asks the decision model whether a finding would survive triage.
// It reads the finding as JSON so the same file can be the one a report is
// drafted from, and writes a line a person can act on.
func triageCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("triage", flag.ContinueOnError)
	fs.SetOutput(stderr)
	data := fs.String("data", "", "data directory (default $HUNTDECK_DATA_DIR, else ./data)")
	path := fs.String("file", "-", "the finding as JSON (\"-\" reads stdin)")
	asJSON := fs.Bool("json", false, "print the verdict as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Loaded the way the server loads, so a key saved in Settings counts: reading
	// only the environment made this command answer "not configured" inside the
	// container, where the key lives in the database.
	dir := *data
	if dir == "" {
		dir = os.Getenv("HUNTDECK_DATA_DIR")
	}
	if dir == "" {
		dir = "data"
	}
	d, err := db.Open(ctx0(), dir+"/huntdeck.db")
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck triage: %v\n", err)
		return 1
	}
	defer func() { _ = d.Close() }()
	cfg, err := overrides.New(store.New(d, time.Now), dir, config.OSSource).Config(ctx0())
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck triage: %v\n", err)
		return 1
	}
	if cfg.JevKey.Reveal() == "" {
		fmt.Fprintln(stderr, "huntdeck triage: no decision model configured: set HUNTDECK_JEV_KEY (Settings > Accounts > Decision model)")
		return 1
	}

	var raw []byte
	if *path == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(*path)
	}
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck triage: %v\n", err)
		return 1
	}
	var f triage.Finding
	if err := json.Unmarshal(raw, &f); err != nil {
		fmt.Fprintf(stderr, "huntdeck triage: %s: %v\n", *path, err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := &jev.Client{BaseURL: cfg.JevURL, Key: cfg.JevKey.Reveal(), Model: cfg.JevModel, Proxy: cfg.JevProxy, Provider: cfg.JevProvider, AccountID: cfg.JevAccountID, WorkspaceID: cfg.JevWorkspaceID}
	v, err := triage.Judge(ctx, client, f)
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck triage: %v\n", err)
		return 1
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(v)
		return 0
	}
	fmt.Fprintf(stdout, "%s\n", v.Call)
	fmt.Fprintf(stdout, "  accepted %.2f · evidence %.2f · %s\n", v.Accept, v.Evidence, v.Model)
	fmt.Fprintf(stdout, "  severity %.2f · %s (%.2f confidence) on the bounty scale · %s\n", v.Severity, triage.SeverityName(v.Severity), v.SeverityConfidence, v.Model)
	fmt.Fprintf(stdout, "  %s\n", v.Why)
	if v.Advice != "" {
		fmt.Fprintf(stdout, "  what would move it: %s (%.2f)\n", v.Advice, v.AdviceConfidence)
	}
	return 0
}

// ctx0 is a plain background context for the setup work that runs before the
// judged call gets its own timeout.
func ctx0() context.Context { return context.Background() }
