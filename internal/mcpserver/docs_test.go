package mcpserver

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// docs/mcp.md names every tool, and no tool it doesn't offer: the page an
// agent's operator reads is kept honest by the same list the protocol serves.
func TestEveryToolIsDocumented(t *testing.T) {
	doc, err := os.ReadFile("../../docs/mcp.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)

	offered := make([]string, 0, len(tools))
	for _, tool := range tools {
		offered = append(offered, tool.Name)
		if !strings.Contains(text, tool.Name) {
			t.Errorf("%s is offered but not in docs/mcp.md", tool.Name)
		}
	}

	listed := regexp.MustCompile(`huntdeck_[a-z_]+`).FindAllString(text, -1)
	for _, name := range listed {
		if !slices.Contains(offered, name) {
			t.Errorf("docs/mcp.md names %s, which the server does not offer", name)
		}
	}
}
