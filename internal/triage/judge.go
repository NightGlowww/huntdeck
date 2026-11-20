package triage

import (
	"context"
	"fmt"
	"strings"

	"github.com/mdpsec/huntdeck/internal/jev"
)

// The three questions travel together: TypeSafe's manual says any mix of
// question types can share one state, evaluated in parallel, and that adding
// questions barely changes the response time. Two nouls and a score in one call
// replaced three round trips through a proxy that flaps.
const (
	qAccept   = "accept"
	qEvidence = "evidence"
	qSeverity = "severity"
)

// SeverityLevels is the rubric, low to high. The wording is what separates the
// levels: a Score answer may fall between two of them, and these say where.
var SeverityLevels = []string{
	"informational: no security impact, or only discloses what is already public",
	"low: real but narrow, with preconditions an attacker rarely controls",
	"medium: real impact on one user or account, reachable with normal access",
	"high: broad impact across users or tenants, or a serious data exposure",
	"critical: full compromise of the service, its data, or many tenants at once",
}

// Judge asks the two probability questions and the severity rating in one call,
// then turns them into one call.
func Judge(ctx context.Context, c *jev.Client, f Finding) (Verdict, error) {
	state, err := stateOf(f)
	if err != nil {
		return Verdict{}, err
	}
	answers, model, err := c.Ask(ctx, state, map[string]jev.Question{
		qAccept:   jev.Noul("Would a bug bounty triager accept this as a valid, in-scope, payable finding?"),
		qEvidence: jev.Noul("Do the supplied artefacts prove the impact as stated?"),
		qSeverity: jev.Score("Rate the severity of this finding on a standard bounty scale.", SeverityLevels),
	})
	if err != nil {
		return Verdict{}, err
	}
	acc, okA := answers[qAccept]
	evi, okE := answers[qEvidence]
	if !okA || !okE {
		return Verdict{}, fmt.Errorf("triage: the decision model did not answer")
	}
	v := classify(acc.Noul, evi.Noul, model)
	if sev, ok := answers[qSeverity]; ok {
		v.Severity = sev.Score
		v.SeverityConfidence = sev.Confidence
		v.SeverityLevels = sev.Probabilities
	}
	// Only when the evidence is the problem: advice about a finding that would
	// be rejected for scope is noise.
	if v.Evidence < evidenceFloor && v.Call != CallRejected {
		v.Advice, v.AdviceConfidence = advise(ctx, c, state)
	}
	return v, nil
}

// stateOf builds the state once, for every question in the call.
func stateOf(f Finding) (map[string]any, error) {
	if strings.TrimSpace(f.Title) == "" || strings.TrimSpace(f.Impact) == "" {
		return nil, fmt.Errorf("triage: a finding needs at least a title and an impact")
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
	return state, nil
}
