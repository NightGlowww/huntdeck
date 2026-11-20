package policycheck

// Rule is one axis a bounty policy usually constrains. The six were chosen
// because every policy read while building this had something to say about
// each, and because one number cannot tell a hunter which line they are about
// to cross.
type Rule struct {
	Key   string
	Label string // what the rule is, in the policy's own terms
}

// Rules are asked one at a time, each as its own probability.
func Rules() []Rule {
	return []Rule{
		{"identity", "the requirement to identify yourself (a registered account, an identifying header, or a platform-provided alias)"},
		{"automation", "the ban on automated scanning or tool-driven testing"},
		{"scope", "the requirement to stay inside the listed scope"},
		{"soceng", "the ban on social engineering, phishing or contacting staff"},
		{"dos", "the ban on denial-of-service or stress testing"},
		{"data", "the requirement not to access other people's real data"},
	}
}

// RuleVerdict is P(the plan breaks this rule).
type RuleVerdict struct {
	Key   string
	Label string
	Break float64
}
