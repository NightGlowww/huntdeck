// Package triage asks a decision model whether a finding would survive triage,
// before it is submitted.
//
// The shapes here were measured rather than guessed. Three questions were tried
// on three findings with known outcomes (one out of scope, one that needs more
// work, one that pays):
//
//	one three-way choice      accept 0.94 / needs_more 0.48 / reject 0.93
//	two binary probabilities  accept 0.84 / 0.23 / 0.10
//	                          evidence 0.86 / 0.22 / 0.58
//
// A three-way choice collapses the middle case onto a coin toss. Two binary
// questions separate both axes - would it be accepted, and does the evidence
// prove the impact - so the answer says which of the two is the problem.
package triage

import (
	"context"
	"fmt"
	"strings"

	"github.com/mdpsec/huntdeck/internal/jev"
)

// Finding is what the submitter knows, in their own words. The model cannot
// browse: it judges exactly this, and nothing more.
type Finding struct {
	Title    string `json:"title"`
	Target   string `json:"target"`
	InScope  string `json:"in_scope"` // the scope situation, in a sentence
	Impact   string `json:"impact"`   // what an attacker gains
	Evidence string `json:"evidence"` // what artefacts exist
	Steps    string `json:"steps,omitempty"`
	Platform string `json:"platform,omitempty"`
	Program  string `json:"program,omitempty"`
}

// Verdict is the two probabilities and what they mean.
type Verdict struct {
	// Accept is P(a triager would accept this as valid, in-scope and payable).
	Accept float64
	// Evidence is P(the supplied artefacts prove the impact as stated).
	Evidence float64
	// Call is submittable, needs_work or likely_rejected.
	Call string
	// Why names the axis that decided it, for the page and the report.
	Why   string
	Model string
}

// Calls a verdict can carry.
const (
	CallSubmittable = "submittable"
	CallNeedsWork   = "needs_work"
	CallRejected    = "likely_rejected"
)

// The thresholds come from the measured separation: the payable example scored
// 0.84 accept with 0.86 evidence, the out-of-scope one 0.10 with 0.58, and the
// half-finished one 0.23 with 0.22.
const (
	acceptFloor   = 0.70
	rejectCeiling = 0.30
	evidenceFloor = 0.60
)

// Judge asks the two questions and turns them into one call.
func Judge(ctx context.Context, c *jev.Client, f Finding) (Verdict, error) {
	if strings.TrimSpace(f.Title) == "" || strings.TrimSpace(f.Impact) == "" {
		return Verdict{}, fmt.Errorf("triage: a finding needs at least a title and an impact")
	}
	state := map[string]any{
		"finding":  f.Title,
		"target":   f.Target,
		"scope":    f.InScope,
		"impact":   f.Impact,
		"evidence": f.Evidence,
	}
	if f.Steps != "" {
		state["steps"] = f.Steps
	}
	if f.Platform != "" {
		state["platform"] = f.Platform
	}
	if f.Program != "" {
		state["program"] = f.Program
	}
	accept, model, err := c.Probability(ctx, state,
		"Would a bug bounty triager accept this as a valid, in-scope, payable finding?")
	if err != nil {
		return Verdict{}, err
	}
	evidence, _, err := c.Probability(ctx, state,
		"Do the supplied artefacts prove the impact as stated?")
	if err != nil {
		return Verdict{}, err
	}
	return classify(accept, evidence, model), nil
}

// classify is the rule, kept apart from the network so it can be tested
// against the measured numbers without a model.
func classify(accept, evidence float64, model string) Verdict {
	v := Verdict{Accept: accept, Evidence: evidence, Model: model}
	switch {
	case accept <= rejectCeiling:
		v.Call = CallRejected
		v.Why = "the triager would likely refuse it: out of scope, not a vulnerability, or not payable"
	case accept < acceptFloor:
		v.Call = CallNeedsWork
		v.Why = "the outcome is genuinely uncertain: strengthen the case before spending a submission"
	case evidence < evidenceFloor:
		v.Call = CallNeedsWork
		v.Why = "likely accepted, but the evidence does not yet prove the impact"
	default:
		v.Call = CallSubmittable
		v.Why = "in scope, payable, and the evidence carries the impact"
	}
	return v
}
