// Package scorecheck asks the decision model whether the figures HuntDeck shows
// for a program are supported by the data it stores.
//
// It does not compute the score: HuntScore is deterministic, and the model has
// no better data than the score already used. What it adds is an audit of the
// claims those figures become on a page.
//
// Measured on one program, six claims against its stored facts:
//
//	"offers a bounty"                 0.96   true
//	"the top bounty is X"             0.87   true
//	"pays up to USD 500,000"          0.07   invented
//	"median payout is 3 days"         0.04   invented
//	"paid USD 2M in 90 days"          0.04   invented
//	"is currently paused"             0.04   false
//
// So the separation is 0.87-0.96 against 0.04-0.07, and the thresholds come from
// that gap rather than from round numbers.
package scorecheck

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/mdpsec/huntdeck/internal/jev"
)

// Facts are the figures as the page shows them, and the stored evidence they
// came from. Both are strings already formatted: this package does not decide
// how a number reads, only whether the evidence backs it.
type Facts struct {
	Program  string            `json:"program"`
	Evidence map[string]string `json:"evidence"` // field -> stored value, verbatim
	Claims   map[string]string `json:"claims"`   // field -> the sentence the page makes of it
}

// Verdict is one claim's audit.
type Verdict struct {
	Field     string
	Claim     string
	Supported float64
	Call      string // supported, unclear, unsupported
	Evidence  string // the stored value it was checked against
}

// Calls a claim can carry.
const (
	CallSupported   = "supported"
	CallUnclear     = "unclear"
	CallUnsupported = "unsupported"
)

// Thresholds from the measured gap: true claims scored 0.87-0.96, invented ones
// 0.04-0.07.
const (
	supportedAt = 0.70
	unsupported = 0.30
)

// Check audits every claim at once; they are independent, so they run together.
func Check(ctx context.Context, c *jev.Client, f Facts) ([]Verdict, error) {
	if len(f.Claims) == 0 {
		return nil, fmt.Errorf("scorecheck: no claims to check")
	}
	// One state: the whole evidence set, so a claim can be judged against all of
	// it rather than in isolation.
	state := map[string]any{"program": f.Program, "stored_evidence": f.Evidence}
	out := make([]Verdict, 0, len(f.Claims))
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	for field, claim := range f.Claims {
		wg.Add(1)
		go func(field, claim string) {
			defer wg.Done()
			p, _, err := c.Probability(ctx, state,
				"Does the stored evidence support this claim: "+claim)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			v := classify(p, field, claim, f.Evidence[field])
			mu.Lock()
			out = append(out, v)
			mu.Unlock()
		}(field, claim)
	}
	wg.Wait()
	if firstErr != nil && len(out) == 0 {
		return nil, firstErr
	}
	// Stable order: a page that shuffles its rows each load is unreadable.
	sortVerdicts(out)
	return out, nil
}

// classify is the rule, kept off the network so it can be pinned to the
// measured numbers.
func classify(p float64, field, claim, evidence string) Verdict {
	v := Verdict{Field: field, Claim: claim, Supported: p, Evidence: evidence}
	switch {
	case p >= supportedAt:
		v.Call = CallSupported
	case p <= unsupported:
		v.Call = CallUnsupported
	default:
		v.Call = CallUnclear
	}
	return v
}

func sortVerdicts(vs []Verdict) {
	for i := 1; i < len(vs); i++ {
		for j := i; j > 0 && strings.ToLower(vs[j].Field) < strings.ToLower(vs[j-1].Field); j-- {
			vs[j], vs[j-1] = vs[j-1], vs[j]
		}
	}
}
