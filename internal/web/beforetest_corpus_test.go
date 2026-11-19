package web

import (
	"strings"
	"testing"
)

// Sentences shaped like the ones real policies use. Each must yield a rule,
// and the rule must quote the sentence rather than paraphrase it.
func TestProhibitionsFromRealWyording(t *testing.T) {
	cases := []struct{ policy, want string }{
		{"Do not perform tests against customers of the service.", "customer"},
		{"Do not attempt to authenticate with leaked credentials.", "leaked credentials"},
		{"Never access non-public systems or data.", "non-public"},
		{"You must not store any PII once the report is submitted.", "pii"},
		{"Do not send unsolicited bulk messages (spam).", "spam"},
		{"Testing that causes denial of service is prohibited.", "denial of service"},
	}
	for _, c := range cases {
		rules := policyProhibitionReports(c.policy)
		if len(rules) == 0 {
			t.Errorf("%q produced no rule", c.policy)
			continue
		}
		if !strings.Contains(strings.ToLower(rules[0].Title), c.want) {
			t.Errorf("%q: rule %q does not name %q", c.policy, rules[0].Title, c.want)
		}
		if !rules[0].Alert || !rules[0].FromPolicy {
			t.Errorf("%q: the rule is neither an alert nor marked as read from the policy", c.policy)
		}
		if strings.TrimSpace(rules[0].Detail) == "" {
			t.Errorf("%q: the rule quotes nothing", c.policy)
		}
	}
}
