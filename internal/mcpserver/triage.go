package mcpserver

import (
	"context"
	"fmt"

	"github.com/mdpsec/huntdeck/internal/jev"
	"github.com/mdpsec/huntdeck/internal/triage"
)

// triage asks the decision model about a finding that is about to be submitted.
// It does not touch the instance: the finding comes in the arguments and the
// answer goes back as text, so this is a read-only tool by nature.
func (s *Server) triage(ctx context.Context, args map[string]any) (string, error) {
	if s.JevKey == "" {
		return "", fmt.Errorf("no decision model configured: set HUNTDECK_JEV_KEY (Settings > Accounts > Decision model)")
	}
	f := triage.Finding{
		Title:    stringArg(args, "title"),
		Target:   stringArg(args, "target"),
		InScope:  stringArg(args, "in_scope"),
		Impact:   stringArg(args, "impact"),
		Evidence: stringArg(args, "evidence"),
		Steps:    stringArg(args, "steps"),
	}
	if f.Title == "" || f.Impact == "" {
		return "", fmt.Errorf("title and impact are both needed: the model judges what it is given, and nothing else")
	}
	client := &jev.Client{BaseURL: s.JevURL, Key: s.JevKey, Model: s.JevModel, Proxy: s.JevProxy}
	v, err := triage.Judge(ctx, client, f)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\naccepted %.2f · evidence %.2f · %s\n%s", v.Call, v.Accept, v.Evidence, v.Model, v.Why), nil
}
