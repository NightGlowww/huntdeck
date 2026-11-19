package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The page's chips are the tool's arguments: a filter that never reaches the
// API would answer "what is worth hunting" with the whole queue.
func TestQueueToolPassesTheChips(t *testing.T) {
	srv := instance(t, "pw")
	s := New(srv.URL, "pw", "test", false)
	out, err := s.callTool(context.Background(), "huntdeck_queue", map[string]any{
		"limit": 1, "platform": "hackerone", "kind": "domain", "added": "7d",
		"paying": true, "one": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Rows []struct {
			Value string `json:"value"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Rows) == 0 {
		t.Fatalf("no rows: %.200s", out)
	}
	sent := d.Rows[0].Value
	for _, want := range []string{"platform=hackerone", "kind=domain", "added=7d", "paying=1", "one=1"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the tool did not send %q: %q", want, sent)
		}
	}
}
