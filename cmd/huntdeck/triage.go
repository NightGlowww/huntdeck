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
	"github.com/mdpsec/huntdeck/internal/jev"
	"github.com/mdpsec/huntdeck/internal/triage"
)

// triageCmd asks the decision model whether a finding would survive triage.
// It reads the finding as JSON so the same file can be the one a report is
// drafted from, and writes a line a person can act on.
func triageCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("triage", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("file", "-", "the finding as JSON (\"-\" reads stdin)")
	asJSON := fs.Bool("json", false, "print the verdict as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(config.OSSource)
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
	client := &jev.Client{BaseURL: cfg.JevURL, Key: cfg.JevKey.Reveal(), Model: cfg.JevModel, Proxy: cfg.JevProxy}
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
	fmt.Fprintf(stdout, "  %s\n", v.Why)
	if v.Advice != "" {
		fmt.Fprintf(stdout, "  what would move it: %s (%.2f)\n", v.Advice, v.AdviceConfidence)
	}
	return 0
}
