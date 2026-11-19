package mcpserver

import (
	"context"
	"strings"
	"testing"
)

// explain=true has to reach the API: the factors are computed there, and a
// tool that silently drops the flag looks like a queue with no breakdown.
func TestQueueExplainReachesTheAPI(t *testing.T) {
	srv := instance(t, "pw")
	s := New(srv.URL, "pw", "test", false)
	plain, err := s.callTool(context.Background(), "huntdeck_queue", map[string]any{"limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "factors") {
		t.Error("factors came back without asking")
	}
	full, err := s.callTool(context.Background(), "huntdeck_queue", map[string]any{"limit": 1, "explain": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(full, "factors") {
		t.Errorf("explain did not reach the API: %.200s", full)
	}
}
