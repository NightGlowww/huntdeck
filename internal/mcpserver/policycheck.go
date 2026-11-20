package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

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
	client := &jev.Client{BaseURL: s.JevURL, Key: s.JevKey, Model: s.JevModel, Proxy: s.JevProxy}
	a, err := policycheck.Check(ctx, client, platform+"/"+handle, policy, plan)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\ncomplies %.2f · %s\n%s", a.Call, a.Comply, a.Model, a.Why), nil
}
