package web

import (
	"html"
	"net/http"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/mcpserver"
)

// /mcp lists the tools from the server's own registry. A page that copies the
// list drifts; this one cannot, and the test says so by comparing the rendered
// page with mcpserver.Catalog().
func TestMCPPageListsEveryTool(t *testing.T) {
	e := newTestEnv(t)
	res, body := e.do(e.client(), http.MethodGet, "/mcp", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("mcp page: %d", res.StatusCode)
	}
	// The templates escape an apostrophe, so compare the page unescaped.
	plain := html.UnescapeString(body)
	catalog := mcpserver.Catalog()
	if len(catalog) < 5 {
		t.Fatalf("catalog has %d tools", len(catalog))
	}
	for _, tool := range catalog {
		if !strings.Contains(plain, tool.Name) {
			t.Errorf("the page does not list %s", tool.Name)
		}
		if tool.Description != "" && !strings.Contains(plain, tool.Description[:min(40, len(tool.Description))]) {
			t.Errorf("the page does not carry %s description", tool.Name)
		}
	}
}
