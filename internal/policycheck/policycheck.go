// Package policycheck asks the decision model whether a testing plan fits a
// program's own policy text.
//
// Of every judgement tried against this codebase's data, this is the one that
// discriminates. Measured on two real policies, one question per rule, a clean
// plan against a reckless one:
//
//	Phabricator  clean 0.05-0.11 on every rule   reckless 0.59-0.98
//	H1 security  clean 0.03-0.10 on every rule   reckless 0.44-0.98
//
// A single "does this comply" answered 0.02 for a forbidden target and 0.92 for
// an owned install, which is right but says nothing about which line was
// crossed; the per-rule matrix does, and its clean baseline is an order of
// magnitude lower.
//
// The six rules go in ONE request: TypeSafe's manual says any mix of questions
// can share a state, that they are evaluated in parallel and in isolation, and
// that adding questions barely changes the response time. Six calls were six
// round trips through a proxy that flaps.
//
// The framings that did NOT work are recorded so they are not rebuilt: a
// per-target "worth starting today" answered 0.44-0.52 for every target, and
// "hunt, watch or skip" for a program answered at 0.22-0.46 confidence.
package policycheck

import (
	"context"
	"fmt"
	"strings"

	"github.com/mdpsec/huntdeck/internal/jev"
)

// Answer is the per-rule matrix and what it adds up to.
type Answer struct {
	Rules []RuleVerdict
	// Unknown names the rules the model did not answer: a hole in the matrix,
	// not a zero.
	Unknown []string
	// Call is allowed, check_first or forbidden, decided by the worst rule.
	Call  string
	Model string
	// Why names the rule that decided it.
	Why string
}

// Calls an answer can carry.
const (
	CallAllowed    = "allowed"
	CallCheckFirst = "check_first"
	CallForbidden  = "forbidden"
)

// Thresholds from the measured split: a clean plan sits at 0.03-0.11, a plan
// that breaks a rule at 0.44-0.98. Nothing lands in between often enough to
// matter, and when it does the answer says "check the rule".
const (
	breakLikely   = 0.80
	breakPossible = 0.40
)

// maxPolicy bounds how much policy text is sent: the model is billed by input
// tokens, and the first few thousand characters carry the rules.
const maxPolicy = 8000

// Check judges one plan against one policy, one rule at a time, in one call.
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
	rules := Rules()
	questions := make(map[string]jev.Question, len(rules))
	for _, r := range rules {
		questions[r.Key] = jev.Noul("Does this plan break this rule as the policy states it: " + r.Label + "?")
	}
	answers, model, err := c.Ask(ctx,
		map[string]any{"program": program, "policy": policy, "plan": plan}, questions)
	if err != nil {
		return Answer{}, err
	}
	var known []RuleVerdict
	var unknown []string
	for _, r := range rules {
		a, ok := answers[r.Key]
		if !ok {
			unknown = append(unknown, r.Key)
			continue
		}
		known = append(known, RuleVerdict{Key: r.Key, Label: r.Label, Break: a.Noul})
	}
	if len(known) == 0 {
		return Answer{}, fmt.Errorf("policycheck: the decision model did not answer")
	}
	a := classify(known, model)
	a.Unknown = unknown
	// A partial matrix is not a clean bill: "no rule reads as broken" is a claim
	// about six rules, and five of them may not have answered. Only a rule that
	// was actually read as broken outranks this.
	if len(unknown) > 0 && a.Call == CallAllowed {
		a.Call = CallCheckFirst
		a.Why = "only part of the matrix answered: the rules that did not answer are not evidence of compliance"
	}
	// Cite the sentence only for the rules that were flagged: a quote for every
	// rule would triple the cost to say nothing new.
	for i := range a.Rules {
		if a.Rules[i].Break < breakPossible {
			continue
		}
		quote, _, err := Cite(ctx, c, policy, a.Rules[i].Label)
		if err == nil {
			a.Rules[i].Quote = quote
		}
	}
	return a, nil
}

// classify is the rule, kept off the network so it can be pinned to the
// measured numbers.
func classify(rules []RuleVerdict, model string) Answer {
	a := Answer{Rules: rules, Model: model, Call: CallAllowed,
		Why: "no rule in this policy reads as broken by the plan"}
	worst := RuleVerdict{}
	for _, r := range rules {
		if r.Break > worst.Break {
			worst = r
		}
	}
	switch {
	case worst.Break >= breakLikely:
		a.Call = CallForbidden
		a.Why = fmt.Sprintf("the plan breaks %s (%.2f): read that rule before sending a request", worst.Key, worst.Break)
	case worst.Break >= breakPossible:
		a.Call = CallCheckFirst
		a.Why = fmt.Sprintf("%s is not settled by the policy (%.2f): check that rule before testing", worst.Key, worst.Break)
	}
	return a
}
