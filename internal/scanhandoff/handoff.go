// Package scanhandoff sends the targets HuntDeck finds to a scanner: one MCP
// task per batch, each target recorded so a re-poll does not queue it twice.
// It lives on its own so the poll loop and the page run the same code.
package scanhandoff

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/mdpsec/huntdeck/internal/scopesentry"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Run creates the tasks and returns how many. manual skips the enabled check:
// the button is the operator saying "now" whatever the schedule says.
func Run(ctx context.Context, st *store.Store, client *scopesentry.Client, manual bool) (int, error) {
	cfg, err := st.ScanConfigRow(ctx)
	if err != nil {
		return 0, err
	}
	if !manual && !cfg.Enabled {
		return 0, nil
	}
	if cfg.TemplateID == "" || cfg.NodeName == "" {
		return 0, fmt.Errorf("pick a template and a node first")
	}
	perTask := cfg.PerTask
	if perTask <= 0 {
		perTask = 50
	}
	cands, err := st.ScanCandidates(ctx, cfg.Kinds, perTask*4)
	if err != nil {
		return 0, err
	}
	if len(cands) == 0 {
		return 0, nil
	}
	created := 0
	for start := 0; start < len(cands); start += perTask {
		end := start + perTask
		if end > len(cands) {
			end = len(cands)
		}
		batch := cands[start:end]
		targets := make([]string, 0, len(batch))
		for _, c := range batch {
			targets = append(targets, c.Target)
		}
		name := "huntdeck-" + time.Now().UTC().Format("20060102-150405") + "-" + strconv.Itoa(created+1)
		id, err := client.CreateTask(ctx, name, cfg.TemplateID, cfg.NodeName, targets)
		if err != nil {
			return created, err
		}
		if err := client.StartTask(ctx, id); err != nil {
			// Created but not started: record it anyway so it is not created
			// twice; starting is a separate act.
			return created, fmt.Errorf("task %s created but not started: %w", id, err)
		}
		if err := st.MarkHandedOff(ctx, batch, id); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}
