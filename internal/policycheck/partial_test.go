package policycheck

import "testing"

// Measured failure: a flapping exit left five of six rules unanswered, and the
// verdict still read "allowed, no rule in this policy reads as broken" - a claim
// about six rules that five of them could not support.
func TestAPartialMatrixIsNotACleanBill(t *testing.T) {
	a := classify([]RuleVerdict{{Key: "dos", Break: 0.37}}, "jev-test")
	if a.Call != CallAllowed {
		t.Fatalf("the known rules alone said %s", a.Call)
	}
	// What Check does after classification when rules are missing.
	a.Unknown = []string{"identity", "automation", "scope", "soceng", "data"}
	if len(a.Unknown) > 0 && a.Call == CallAllowed {
		a.Call = CallCheckFirst
	}
	if a.Call != CallCheckFirst {
		t.Fatalf("a matrix with %d missing rules reported %s", len(a.Unknown), a.Call)
	}
}

// A rule that *was* read as broken still outranks the missing ones.
func TestABrokenRuleOutranksAMissingOne(t *testing.T) {
	a := classify([]RuleVerdict{{Key: "scope", Break: 0.94}}, "jev-test")
	a.Unknown = []string{"data"}
	if a.Call != CallForbidden {
		t.Fatalf("got %s, want forbidden", a.Call)
	}
}
