package triage

import "testing"

// The three measured readings, plus the boundaries. 3.17 is the cross-tenant
// invoice read: the model put most of its mass on "high" and some on "critical",
// so the summary says high - the number alone would not tell a reader that.
func TestSeverityNameMatchesTheMeasuredReadings(t *testing.T) {
	cases := []struct {
		score float64
		want  string
	}{
		{1.04, "low"},
		{1.06, "low"},
		{3.17, "high"},
		{0, "informational"},
		{0.49, "informational"},
		{0.50, "low"},
		{4.6, "critical"},
		{-1, "informational"},
	}
	for _, c := range cases {
		if got := SeverityName(c.score); got != c.want {
			t.Errorf("SeverityName(%.2f) = %q, want %q", c.score, got, c.want)
		}
	}
}

// The names and the rubric must not drift apart: five levels, five names.
func TestSeverityNamesCoverTheLevels(t *testing.T) {
	if len(SeverityNames) != len(SeverityLevels) {
		t.Fatalf("%d names for %d levels", len(SeverityNames), len(SeverityLevels))
	}
}
