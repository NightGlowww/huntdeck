package scorecheck

import "testing"

// The rule is pinned to the six measured claims: true ones at 0.87 and 0.96,
// invented ones at 0.04-0.07. Moving a threshold must move the case it was
// chosen for, which is why the numbers are in the test rather than rounded.
func TestClassifyMatchesTheMeasuredClaims(t *testing.T) {
	cases := []struct {
		name string
		p    float64
		want string
	}{
		{"offers a bounty, measured 0.96", 0.96, CallSupported},
		{"top bounty is X, measured 0.87", 0.87, CallSupported},
		{"right at the supported line", 0.70, CallSupported},
		{"between, 0.55", 0.55, CallUnclear},
		{"invented price, measured 0.07", 0.07, CallUnsupported},
		{"invented median, measured 0.04", 0.04, CallUnsupported},
		{"right at the unsupported line", 0.30, CallUnsupported},
		{"just above it, 0.31", 0.31, CallUnclear},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := classify(c.p, "bounty_max", "the top bounty is 200k", "200000")
			if v.Call != c.want {
				t.Fatalf("%.2f gave %s, want %s", c.p, v.Call, c.want)
			}
			if v.Evidence != "200000" {
				t.Error("the verdict lost the evidence it was checked against")
			}
		})
	}
}

// An empty set of claims is not a clean audit.
func TestCheckRefusesAnEmptyCase(t *testing.T) {
	if _, err := Check(t.Context(), nil, Facts{Program: "x"}); err == nil {
		t.Fatal("an audit with no claims was accepted")
	}
}
