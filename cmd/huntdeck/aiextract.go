package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/mdpsec/huntdeck/internal/policyai"

	"github.com/mdpsec/huntdeck/internal/ai"
	"github.com/mdpsec/huntdeck/internal/config"
)

// aiExtractCmd asks the configured model to read each program's policy and keeps
// the rules it finds, cached by the policy's hash. It runs here rather than on a
// page render so no page waits on a network call, and it is opt-in: without
// HUNTDECK_AI_* it says so and stops.
func aiExtractCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ai-extract", flag.ContinueOnError)
	fs.SetOutput(stderr)
	limit := fs.Int("limit", 25, "how many programs to read in this run")
	force := fs.Bool("force", false, "read again even when the policy has not changed")
	clear := fs.Bool("clear", false, "drop every cached reading and exit")
	platform := fs.String("platform", "", "only this platform")
	if _, code, ok := parseFlags(fs, args, stderr); !ok {
		return code
	}
	ctx, stop := signalContext()
	defer stop()
	if *clear {
		app, ok := openApp(ctx, stderr, true)
		if !ok {
			return 1
		}
		defer app.close()
		n, err := app.store.ClearAIRules(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "huntdeck ai-extract: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "cleared %d cached reading(s)\n", n)
		return 0
	}
	cfg, err := config.Load(config.OSSource)
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck ai-extract: %v\n", err)
		return 1
	}
	client := &ai.Client{BaseURL: cfg.AIBaseURL, APIKey: cfg.AIKey.Reveal(), Model: cfg.AIModel}
	if !client.Configured() {
		fmt.Fprintln(stderr, "huntdeck ai-extract: no endpoint configured (HUNTDECK_AI_BASE_URL and HUNTDECK_AI_MODEL)")
		return 2
	}
	app, ok := openApp(ctx, stderr, true)
	if !ok {
		return 1
	}
	defer app.close()

	client2 := &ai.Client{BaseURL: cfg.AIBaseURL, APIKey: cfg.AIKey.Reveal(), Model: cfg.AIModel}
	res, err := policyai.Extract(ctx, app.store, client2, policyai.Options{Platform: *platform, Limit: *limit, Force: *force})
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck ai-extract: %v\n", err)
		return 1
	}
	for _, e := range res.Errors {
		fmt.Fprintf(stderr, "  %s\n", e)
	}
	fmt.Fprintf(stdout, "read %d, skipped %d, failed %d\n", res.Read, res.Skipped, res.Failed)
	return 0
}
