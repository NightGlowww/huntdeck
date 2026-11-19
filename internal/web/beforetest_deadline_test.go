package web

import (
	"testing"
)

// A response promise is not a reporting deadline.
func TestPolicyDeadlineIgnoresResponsePromises(t *testing.T) {
	if got := policyDeadline("Reports are reviewed within 15 days at most."); got != nil {
		t.Errorf("a review promise became a deadline: %+v", got)
	}
	if got := policyDeadline("You must submit your report within 30 days of discovery."); len(got) != 1 {
		t.Errorf("a real deadline was missed: %+v", got)
	}
}
