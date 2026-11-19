package web

import (
	"strings"
	"testing"
)

// A response promise earlier in the text must not hide the reporting deadline
// that follows it.
func TestPolicyDeadlineFindsTheRealOneAfterAPromise(t *testing.T) {
	text := "Reports are reviewed within 15 days. You must submit your report within 30 days of discovery."
	got := policyDeadline(text)
	if len(got) != 1 {
		t.Fatalf("the deadline after a promise was dropped: %+v", got)
	}
	if !containsFold(got[0].Title, "30 days") {
		t.Errorf("picked the wrong sentence: %q", got[0].Title)
	}
}

func containsFold(s, sub string) bool {
	return len(s) >= len(sub) && strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
