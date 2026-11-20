package triage

import "testing"

// The three measured findings, with the numbers the model actually returned.
// The rule is pinned to them: if a threshold moves, the case it was chosen for
// changes verdict, and that is the point of writing them down.
func TestClassifyMatchesTheMeasuredCases(t *testing.T) {
	cases := []struct {
		name     string
		accept   float64
		evidence float64
		want     string
	}{
		{"out of scope, model 0.10/0.58", 0.10, 0.58, CallRejected},
		{"half finished, model 0.23/0.22", 0.23, 0.22, CallRejected},
		{"uncertain but in scope, 0.55/0.70", 0.55, 0.70, CallNeedsWork},
		{"payable but unproven, 0.84/0.40", 0.84, 0.40, CallNeedsWork},
		{"the payable example, 0.84/0.86", 0.84, 0.86, CallSubmittable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := classify(c.accept, c.evidence, "jev-test")
			if v.Call != c.want {
				t.Fatalf("%.2f/%.2f gave %s, want %s (why: %s)", c.accept, c.evidence, v.Call, c.want, v.Why)
			}
			if v.Why == "" {
				t.Error("a verdict without a reason cannot be acted on")
			}
		})
	}
}

// A finding with no impact is not judgeable: the model would be scoring a
// sentence about nothing.
func TestJudgeRefusesAnEmptyCase(t *testing.T) {
	if _, err := Judge(t.Context(), nil, Finding{Title: "x"}); err == nil {
		t.Fatal("an empty impact was accepted")
	}
}
