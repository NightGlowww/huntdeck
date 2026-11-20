package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mdpsec/huntdeck/internal/jev"
	"github.com/mdpsec/huntdeck/internal/policycheck"
)

// policyCheck asks the decision model whether a testing plan fits a program's
// own policy. The policy comes from the instance (the same text its Policy tab
// shows); the plan comes from the caller.
func (s *Server) policyCheck(ctx context.Context, args map[string]any) (string, error) {
	if s.JevKey == "" {
		return "", fmt.Errorf("no decision model configured: set HUNTDECK_JEV_KEY (Settings > Accounts > Decision model)")
	}
	platform, handle := stringArg(args, "platform"), stringArg(args, "handle")
	plan := stringArg(args, "plan")
	if platform == "" || handle == "" || plan == "" {
		return "", fmt.Errorf("platform, handle and plan are all needed")
	}
	raw, err := s.get(ctx, "/api/v1/programs/"+platform+"/"+handle, nil)
	if err != nil {
		return "", err
	}
	var doc struct {
		Brief struct {
			Policy string `json:"policy"`
		} `json:"brief"`
	}
	policy := ""
	if json.Unmarshal([]byte(raw), &doc) == nil {
		policy = doc.Brief.Policy
	}
	if policy == "" {
		return "", fmt.Errorf("%s/%s has no policy text stored: poll it, or read the policy on its page", platform, handle)
	}
	client := &jev.Client{BaseURL: s.JevURL, Key: s.JevKey, Model: s.JevModel, Proxy: s.JevProxy, Provider: s.JevProvider, AccountID: s.JevAccountID, WorkspaceID: s.JevWorkspaceID}
	a, err := policycheck.Check(ctx, client, platform+"/"+handle, policy, plan)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %s\n%s", a.Call, a.Model, a.Why)
	for _, r := range a.Rules {
		if r.Break >= 0.05 {
			fmt.Fprintf(&b, "\n  %-12s %.2f", r.Key, r.Break)
		}
		if r.Quote != "" {
			fmt.Fprintf(&b, "\n               %q", r.Quote)
		}
	}
	if len(a.Unknown) > 0 {
		fmt.Fprintf(&b, "\n  not answered: %s", strings.Join(a.Unknown, ", "))
	}
	return b.String(), nil
}
