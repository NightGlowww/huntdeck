package policycheck

import "testing"

// The rule is pinned to the two measured matrices. A clean plan scored 0.03-0.11
// on every rule; a reckless one scored 0.44-0.98. Moving a threshold changes the
// verdict for the case it was chosen for, which is why the numbers are written
// down rather than rounded.
func TestClassifyMatchesTheMeasuredMatrices(t *testing.T) {
	cases := []struct {
		name  string
		rules map[string]float64
		want  string
	}{
		{"clean plan, Phabricator 0.05-0.11", map[string]float64{
			"identity": 0.09, "automation": 0.10, "soceng": 0.06, "dos": 0.05, "data": 0.11}, CallAllowed},
		{"clean plan, H1 security 0.03-0.10", map[string]float64{
			"identity": 0.06, "automation": 0.05, "scope": 0.10, "soceng": 0.05, "dos": 0.03, "data": 0.06}, CallAllowed},
		{"reckless plan, scope 0.98", map[string]float64{
			"identity": 0.81, "automation": 0.85, "scope": 0.98, "soceng": 0.59, "dos": 0.95, "data": 0.94}, CallForbidden},
		{"reckless plan, automation 0.97", map[string]float64{
			"identity": 0.61, "automation": 0.97, "scope": 0.98, "soceng": 0.44, "dos": 0.97, "data": 0.81}, CallForbidden},
		{"one rule unsettled at 0.55", map[string]float64{
			"identity": 0.08, "automation": 0.55, "dos": 0.05}, CallCheckFirst},
		{"one rule right at the forbidden line", map[string]float64{
			"identity": 0.08, "automation": 0.80}, CallForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var rules []RuleVerdict
			for k, v := range c.rules {
				rules = append(rules, RuleVerdict{Key: k, Break: v})
			}
			a := classify(rules, "jev-test")
			if a.Call != c.want {
				t.Fatalf("got %s, want %s (%s)", a.Call, c.want, a.Why)
			}
			if a.Why == "" {
				t.Error("an answer without a reason cannot be acted on")
			}
		})
	}
}

// The matrix has to name the worst rule, or "forbidden" says nothing about
// which line was crossed.
func TestClassifyNamesTheWorstRule(t *testing.T) {
	a := classify([]RuleVerdict{
		{Key: "identity", Break: 0.05},
		{Key: "automation", Break: 0.93},
		{Key: "dos", Break: 0.40},
	}, "jev-test")
	if a.Call != CallForbidden || !contains(a.Why, "automation") {
		t.Fatalf("got %s %q, want forbidden naming automation", a.Call, a.Why)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// A policy that is not stored, or a plan that says nothing, is not judgeable.
func TestCheckRefusesAnEmptyCase(t *testing.T) {
	if _, err := Check(t.Context(), nil, "acme", "", "scan it"); err == nil {
		t.Fatal("an empty policy was accepted")
	}
	if _, err := Check(t.Context(), nil, "acme", "a policy", "  "); err == nil {
		t.Fatal("an empty plan was accepted")
	}
}
