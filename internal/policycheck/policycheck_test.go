package policycheck

import "testing"

// The rule is pinned to the two measured pairs. Moving a threshold changes the
// verdict for the case it was chosen for, which is the point of writing the
// numbers down rather than choosing round ones.
func TestClassifyMatchesTheMeasuredPairs(t *testing.T) {
	cases := []struct {
		name string
		p    float64
		want string
	}{
		{"forbidden target, measured 0.02", 0.02, CallForbidden},
		{"missing required header, measured 0.03", 0.03, CallForbidden},
		{"compliant plan, measured 0.43", 0.43, CallCheckFirst},
		{"unsettled, 0.75", 0.75, CallCheckFirst},
		{"owned install, measured 0.92", 0.92, CallAllowed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := classify(c.p, "jev-test")
			if a.Call != c.want {
				t.Fatalf("%.2f gave %s, want %s (%s)", c.p, a.Call, c.want, a.Why)
			}
			if a.Why == "" {
				t.Error("an answer without a reason cannot be acted on")
			}
		})
	}
}

// A policy that is not stored, or a plan that says nothing, is not judgeable:
// the model would be scoring an empty question.
func TestCheckRefusesAnEmptyCase(t *testing.T) {
	if _, err := Check(t.Context(), nil, "acme", "", "scan it"); err == nil {
		t.Fatal("an empty policy was accepted")
	}
	if _, err := Check(t.Context(), nil, "acme", "a policy", "  "); err == nil {
		t.Fatal("an empty plan was accepted")
	}
}
