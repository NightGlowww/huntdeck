// Package policycheck asks the decision model one question about a program's
// own policy text: does this testing plan comply with it.
//
// Of every judgement tried against this codebase's data, this is the one that
// discriminates. Measured on two real policies:
//
//	Phabricator: test secure.phabricator.com (the policy forbids it)  0.02
//	Phabricator: test an install the hunter owns                     0.92
//	HackerOne security: scan without the required identifying header  0.03
//	HackerOne security: scan with the header                          0.43
//
// The framings that did NOT work are recorded here so they are not rebuilt: a
// per-target "worth starting today" answered 0.44-0.52 for every target, and
// "hunt, watch or skip" for a program answered at 0.22-0.46 confidence. Both
// restate the aggregate score the queue already gives, and the queue has no
// target-specific evidence for the model to weigh.
package policycheck

import (
	"context"
	"fmt"
	"strings"

	"github.com/mdpsec/huntdeck/internal/jev"
)

// Answer is the probability that the plan complies, and what to do about it.
type Answer struct {
	// Comply is P(the plan complies with this policy).
	Comply float64
	// Call is allowed, check_first or forbidden.
	Call  string
	Model string
	// Why is one sentence a person can act on.
	Why string
}

// Calls an answer can carry. The thresholds sit on the measured pairs: the
// forbidden case scored 0.02-0.03, the clearly allowed one 0.92.
const (
	CallAllowed    = "allowed"
	CallCheckFirst = "check_first"
	CallForbidden  = "forbidden"
)

const (
	forbiddenBelow = 0.20
	allowedAt      = 0.80
)

// maxPolicy bounds how much policy text is sent: the model is billed by input
// tokens, and the first few thousand characters carry the rules.
const maxPolicy = 8000

// Check judges one plan against one policy.
func Check(ctx context.Context, c *jev.Client, program, policy, plan string) (Answer, error) {
	if strings.TrimSpace(policy) == "" {
		return Answer{}, fmt.Errorf("policycheck: no policy text stored for %s", program)
	}
	if strings.TrimSpace(plan) == "" {
		return Answer{}, fmt.Errorf("policycheck: say what you plan to do")
	}
	if len(policy) > maxPolicy {
		policy = policy[:maxPolicy]
	}
	p, model, err := c.Probability(ctx,
		map[string]any{"program": program, "policy": policy, "plan": plan},
		"Does this testing plan comply with this program's policy?")
	if err != nil {
		return Answer{}, err
	}
	return classify(p, model), nil
}

// classify is the rule, kept off the network so it can be pinned to the
// measured numbers.
func classify(p float64, model string) Answer {
	a := Answer{Comply: p, Model: model}
	switch {
	case p < forbiddenBelow:
		a.Call = CallForbidden
		a.Why = "the policy reads as forbidding this: read it before sending a request"
	case p >= allowedAt:
		a.Call = CallAllowed
		a.Why = "the policy allows this plan as described"
	default:
		a.Call = CallCheckFirst
		a.Why = "the policy does not settle it: check the quoted rules before testing"
	}
	return a
}
