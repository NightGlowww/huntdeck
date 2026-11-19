package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/mdpsec/huntdeck/internal/ai"
	"github.com/mdpsec/huntdeck/internal/config"
)

// aiCheckCmd asks the configured endpoint one question and reports whether the
// key, the model and the endpoint agree. It is the first thing to run after
// setting HUNTDECK_AI_*, before any feature depends on them.
func aiCheckCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ai-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	_ = fs.Parse(args)

	cfg, err := config.Load(config.OSSource)
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck ai-check: %v\n", err)
		return 1
	}
	client := &ai.Client{BaseURL: cfg.AIBaseURL, APIKey: cfg.AIKey.Reveal(), Model: cfg.AIModel}
	if !client.Configured() {
		fmt.Fprintln(stderr, "huntdeck ai-check: no endpoint configured (HUNTDECK_AI_BASE_URL and HUNTDECK_AI_MODEL)")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out struct {
		Answer string `json:"answer"`
	}
	start := time.Now()
	if err := client.CompleteJSON(ctx,
		"You answer with JSON only.",
		`Reply with {"answer":"ok"} and nothing else.`, &out); err != nil {
		fmt.Fprintf(stderr, "huntdeck ai-check: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s · %s answered in %s: %q\n", cfg.AIBaseURL, cfg.AIModel, time.Since(start).Round(time.Millisecond), out.Answer)
	return 0
}
