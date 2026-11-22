package scanhandoff

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mdpsec/huntdeck/internal/jev"
	"github.com/mdpsec/huntdeck/internal/scopesentry"
	"github.com/mdpsec/huntdeck/internal/store"
)

// MinFreePercent is the headroom the scanner's own host must have before
// HuntDeck hands it more work. A scan task is expensive: on a 3.6G box one
// full-template nuclei run took the machine down for hours, so the handoff
// stays out of the way when memory is short.
const MinFreePercent = 25.0

// headroom reports the scanner host's free memory, when it can be read.
// ScopeSentry answers this on /healthz; a failure is not a green light.
var headroom = func(client *scopesentry.Client) (float64, bool, error) {
	return client.MemAvailablePercent(context.Background())
}

// CheckHeadroom refuses a handoff when the scanner host is short on memory.
func CheckHeadroom(ctx context.Context, client *scopesentry.Client) error {
	pct, ok, err := headroom(client)
	if err != nil {
		// A paused or stopped container is the usual cause here, and the timeout it
		// produces says nothing about that: name it, so nobody debugs the network.
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Timeout exceeded") {
			return fmt.Errorf("the scanner is not answering at %s (paused or stopped?): %w", client.BaseURL, err)
		}
		return fmt.Errorf("cannot read the scanner's memory headroom: %w", err)
	}
	if !ok {
		// Unknown is not permission. The comment said so and the code let it
		// through: a scanner that does not report its memory got a free pass.
		return fmt.Errorf("the scanner did not report its memory headroom: no new scans")
	}
	if pct < MinFreePercent {
		return fmt.Errorf("the scanner host has %.0f%% memory free (needs %.0f%%): no new scans", pct, MinFreePercent)
	}
	return nil
}

// SecondOpinion is the judgement a decision model gives before a batch is
// handed over. It is advisory by construction: the deterministic floor runs
// first, and this only ever narrows what is allowed, never widens it.
type SecondOpinion interface {
	Judge(ctx context.Context, s State) (Verdict, error)
}

// State is what the model is told: the shape of the batch and the conditions
// that have bitten before.
type State struct {
	Tasks       int
	Targets     int
	PerTask     int
	Template    string
	Node        string
	HostFreePct float64
	HostKnown   bool
}

// Verdict is one answered judgement.
type Verdict struct {
	Choice        string
	Confidence    float64
	Probabilities map[string]float64
	Model         string
}

// Choices the handoff understands.
const (
	ActProceed      = "proceed"
	ActConfirmFirst = "confirm_first"
	ActSplitSmaller = "split_smaller"
	ActBlock        = "block"
)

// jevOpinion adapts the Jev client to SecondOpinion.
type jevOpinion struct{ c *jev.Client }

// NewJevOpinion asks TypeSafe's System One model.
func NewJevOpinion(c *jev.Client) SecondOpinion { return jevOpinion{c: c} }

func (j jevOpinion) Judge(ctx context.Context, s State) (Verdict, error) {
	state := map[string]any{
		"action":                   "create scanner tasks that run nuclei against in-scope targets",
		"tasks":                    s.Tasks,
		"targets":                  s.Targets,
		"targets_per_task":         s.PerTask,
		"template":                 s.Template,
		"node":                     s.Node,
		"host_memory_free_percent": s.HostFreePct,
		"history":                  "two OOM kills of scan processes on this host in the past week; the scanner container has since held a 2.4GiB memory limit and stayed within it for 24h",
		"policy":                   "a scan is the heaviest thing either side runs; the scanner should have its own host",
	}
	v, err := j.c.Choice(ctx, state,
		"Should this batch of scan tasks be created right now?",
		map[string]string{
			ActProceed:      "the batch is safe to run unattended now",
			ActConfirmFirst: "someone should look before this runs",
			ActSplitSmaller: "it can run, but smaller than this",
			ActBlock:        "this must not run now, on these conditions",
		})
	if err != nil {
		return Verdict{}, err
	}
	return Verdict{Choice: v.Choice, Confidence: v.Confidence, Probabilities: v.Probabilities, Model: v.Model}, nil
}

var _ = store.ScanConfig{}
