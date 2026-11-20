package mcpserver

import (
	"context"
	"strings"
	"testing"
)

// The JSON sweep skips local tools because they never ask the instance, so this
// one needs its own cover: without a decision model it must say so plainly
// rather than answering an empty string, and it must refuse a case with nothing
// to judge.
func TestTriageToolSaysWhatItNeeds(t *testing.T) {
	s := New("http://127.0.0.1:1", "pw", "test", false)
	if _, err := s.callTool(context.Background(), "huntdeck_triage",
		map[string]any{"title": "x", "in_scope": "in", "impact": "y", "evidence": "z"}); err == nil ||
		!strings.Contains(err.Error(), "no decision model configured") {
		t.Fatalf("a server without a model answered %v", err)
	}

	s.JevKey = "test-key"
	if _, err := s.callTool(context.Background(), "huntdeck_triage",
		map[string]any{"in_scope": "in", "evidence": "z"}); err == nil ||
		!strings.Contains(err.Error(), "title and impact") {
		t.Fatalf("a finding with no title was judged: %v", err)
	}
}
