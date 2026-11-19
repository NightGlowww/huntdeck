package scopesentry

import (
	"context"
	"os"
	"testing"
)

// A live probe, skipped unless SCAN_URL/SCAN_KEY are set: templates and nodes
// as the real server answers them, so the parsing is checked against the wire
// shape rather than a fixture I also wrote.
func TestLiveProbe(t *testing.T) {
	base, key := os.Getenv("SCAN_URL"), os.Getenv("SCAN_KEY")
	if base == "" || key == "" {
		t.Skip("no SCAN_URL/SCAN_KEY")
	}
	c := &Client{BaseURL: base, Key: key}
	ctx := context.Background()
	tools, err := c.Tools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("tools: %v", tools)
	tpl, err := c.Templates(ctx)
	if err != nil {
		t.Fatalf("templates: %v", err)
	}
	t.Logf("templates: %+v", tpl)
	nodes, err := c.Nodes(ctx)
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	t.Logf("nodes: %+v", nodes)
}
