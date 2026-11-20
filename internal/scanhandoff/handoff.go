// Package scanhandoff sends the targets HuntDeck finds to a scanner: one MCP
// task per batch, each target recorded so a re-poll does not queue it twice.
// It lives on its own so the poll loop and the page run the same code.
package scanhandoff

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/mdpsec/huntdeck/internal/scopesentry"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Run creates the tasks and returns how many. manual skips the enabled check:
// the button is the operator saying "now" whatever the schedule says.
// Options are what the caller can supply beyond the store and the scanner.
// Opinion is optional and advisory: nil means the deterministic checks are the
// whole decision.
type Options struct {
	Opinion SecondOpinion
}

// MinInterval is the gap a scheduled run leaves between handoffs. The scanner
// runs one task per node at a time, so a second batch inside this window only
// fills its queue; the operator's "send now" is not held back by it.
const MinInterval = 60 * time.Second

// ErrBusy is the scanner refusing work because its own queue is full or its
// creation limit is reached. It is capacity, not breakage: the operator's answer
// is to wait, and the page says so in words rather than in red.
var ErrBusy = errors.New("the scanner is queued: nothing was created, try again when it has caught up")

func Run(ctx context.Context, st *store.Store, client *scopesentry.Client, manual bool) (int, error) {
	return RunWith(ctx, st, client, manual, Options{})
}

// RunWith is Run with a second opinion: a decision model is asked about the
// batch before anything is created, and its block or confirm answer is
// respected. It can only narrow what the deterministic checks allow.
func RunWith(ctx context.Context, st *store.Store, client *scopesentry.Client, manual bool, opt Options) (int, error) {
	cfg, err := st.ScanConfigRow(ctx)
	if err != nil {
		return 0, err
	}
	// A scheduled run leaves a gap: the scanner runs one task per node at a
	// time, so handing over again before it has started the last batch only
	// fills its queue. The operator pressing "send now" is not held back.
	_, last, _ := st.ScanHandoffCount(ctx)
	if !manual && !last.IsZero() && time.Since(last) < MinInterval {
		return 0, nil
	}
	if !manual && !cfg.Enabled {
		return 0, nil
	}
	if cfg.TemplateID == "" || cfg.NodeName == "" {
		return 0, fmt.Errorf("pick a template and a node first")
	}
	// The scanner's host has to have room for what we are about to ask of it.
	if err := CheckHeadroom(ctx, client); err != nil {
		return 0, err
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
	// The second opinion, if one is configured, is asked once about the whole
	// batch: a per-task question would cost a call each and answer the same
	// thing.
	if opt.Opinion != nil {
		free, known, _ := headroom(client)
		if err := runOpinion(ctx, opt.Opinion, State{
			Tasks: (len(cands) + perTask - 1) / perTask, Targets: len(cands), PerTask: perTask,
			Template: cfg.TemplateID, Node: cfg.NodeName, HostFreePct: free, HostKnown: known,
		}); err != nil {
			return 0, err
		}
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
			// The scanner refusing work for capacity is not a fault: stop here
			// and say so, rather than reporting a failure the operator cannot act on.
			if scopesentry.IsBusy(err) {
				return created, fmt.Errorf("%w: %w", ErrBusy, err)
			}
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

// runOpinion asks the model and turns its answer into a verdict the handoff
// obeys. It is separate from RunWith so the rule - block and confirm stop
// the batch, an unanswered model is not permission - can be tested without
// a store, a scanner or a network.
func runOpinion(ctx context.Context, opinion SecondOpinion, st State) error {
	// A model that cannot be reached has not judged the batch. Refusing every
	// handoff because a third party is unreachable or geo-blocked made the
	// whole feature unusable, which is worse than losing the advisory: the
	// deterministic checks still govern, and they are the ones that matter.
	v, err := opinion.Judge(ctx, st)
	if err != nil {
		slog.WarnContext(ctx, "scan: the second opinion did not answer", "error", err)
		return nil
	}
	// One line either way: without it, a proceed leaves no trace and "did the
	// model run?" can only be answered by the absence of a warning.
	slog.InfoContext(ctx, "scan: second opinion", "choice", v.Choice, "confidence", v.Confidence, "model", v.Model)
	switch v.Choice {
	case ActBlock, ActConfirmFirst:
		return fmt.Errorf("the decision model says %s (confidence %.2f): no scan tasks created", v.Choice, v.Confidence)
	}
	return nil
}
