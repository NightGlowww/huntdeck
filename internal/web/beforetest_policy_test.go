package web

import (
	"strings"
	"testing"
)

// The policy text carries things the platforms' fields do not: what is
// forbidden, how long a report stays valid, and what an account must be.
func TestPolicyRulesBeyondTheHeader(t *testing.T) {
	text := "Do not run any denial of service testing. " +
		"Reports must be submitted within 30 days of discovery. " +
		"You must use a test account and never touch production data."
	rules := policyRules(text)
	seek := func(kind, needle string) bool {
		for _, r := range rules {
			if r.Kind == kind && strings.Contains(strings.ToLower(r.Title), needle) {
				return true
			}
		}
		return false
	}
	if !seek("warn", "denial of service") {
		t.Errorf("the prohibition was not read: %+v", rules)
	}
	if !seek("req", "within 30 days") {
		t.Errorf("the reporting window was not read: %+v", rules)
	}
	if !seek("creds", "test account") {
		t.Errorf("the account rule was not read: %+v", rules)
	}
	for _, r := range rules {
		if r.FromPolicy && r.Detail == "" {
			t.Errorf("rule %q quotes nothing", r.Title)
		}
	}
}
