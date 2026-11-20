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
	// Severity is a position on SeverityLevels, 0 at the low end and 1 at
	// the high end; SeverityLevels is the probability of each level.
	Severity           float64
	SeverityConfidence float64
	SeverityLevels     map[string]float64

	// Why names the axis that decided it, for the page and the report.
	Why string
	// Advice is what would most raise the evidential strength, when the model
	// was sure enough to name it; AdviceConfidence says how sure.
	Advice           string
	AdviceConfidence float64
	Model            string
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

// adviceFloor is the confidence below which the model's suggestion is not
// shown. Measured on one finding across five evidence strengths: it named
// "prove it from a second identity" at 0.92 when the report had single-account
// traffic - the one addition that moved the evidence score from 0.22 to 0.72 -
// and answered at 0.18-0.35 everywhere else, where it would be guessing.
const adviceFloor = 0.60

// The additions the advisor chooses between.
const (
	AdviceSecondIdentity = "prove_it_from_a_second_identity"
	AdviceRawTraffic     = "attach_raw_traffic"
	AdviceSteps          = "write_reproduction_steps"
	AdviceVideo          = "record_a_video"
	AdviceScope          = "cite_the_scope_entry"
	AdviceNothing        = "nothing_more_needed"
)

// SeverityNames are the short names of SeverityLevels, in the same order. A
// score of 3.17 is "high, leaning critical" to the model but "3.17" to a reader:
// the name is what the number means.
var SeverityNames = []string{"informational", "low", "medium", "high", "critical"}

// SeverityName turns a score into the nearest level's name. It rounds rather than
// truncating: 3.17 is nearer high (3) than critical (4), and reporting "high" for
// a reading that leans that way is the honest summary.
func SeverityName(score float64) string {
	if len(SeverityNames) == 0 {
		return ""
	}
	i := int(score + 0.5)
	if i < 0 {
		i = 0
	}
	if i >= len(SeverityNames) {
		i = len(SeverityNames) - 1
	}
	return SeverityNames[i]
}
