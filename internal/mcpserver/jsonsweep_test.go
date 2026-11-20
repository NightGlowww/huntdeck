package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// One fake answered with invalid JSON for weeks and no test noticed, because
// they all used strings.Contains. Every tool that answers JSON is parsed here:
// an unparsable fake verifies nothing about the tool on top of it.
func TestEveryJSONToolParsesAgainstTheFake(t *testing.T) {
	srv := instance(t, "pw")
	s := New(srv.URL, "pw", "test", false)
	// The text tools answer plain text on purpose.
	// The text tools answer plain text on purpose; the match tools have their
	// own focused tests (the fake checks their request shape).
	text := map[string]bool{"huntdeck_scope_text": true, "huntdeck_changes_text": true, "huntdeck_programs_csv": true, "huntdeck_export": true, "huntdeck_match": true, "huntdeck_match_many": true}
	for _, tool := range tools {
		if text[tool.Name] || tool.Write || tool.Local {
			continue
		}
		out, err := s.callTool(context.Background(), tool.Name, argsFor(tool.Name))
		if err != nil {
			t.Errorf("%s: %v", tool.Name, err)
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(out), "{") || strings.HasPrefix(strings.TrimSpace(out), "[") {
			var v any
			if err := json.Unmarshal([]byte(out), &v); err != nil {
				t.Errorf("%s answered invalid JSON: %v\n%.200s", tool.Name, err, out)
			}
		}
	}
}

// argsFor gives each tool the one required argument it insists on.
func argsFor(name string) map[string]any {
	switch name {
	case "huntdeck_program":
		return map[string]any{"platform": "hackerone", "handle": "acme"}
	case "huntdeck_programs":
		// The fake checks this one's search term.
		return map[string]any{"q": "acme", "limit": 1}
	}
	return map[string]any{"limit": 1}
}
