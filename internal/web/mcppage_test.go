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
	// A table built from the schema object instead of its properties listed
	// additionalProperties, properties and required as arguments. "type" is left
	// out on purpose: one tool has an argument of that name.
	for _, keyword := range []string{"additionalProperties", "properties", "required"} {
		if strings.Contains(plain, "<code>"+keyword+"</code>") {
			t.Errorf("the page lists the schema keyword %q as an argument", keyword)
		}
	}
	// The required marks are checked against a written-down expectation, not
	// against Catalog() itself: a guard that reads its expectation from the code
	// under test passes when that code stops populating the field.
	known := map[string][]string{
		"huntdeck_program": {"handle", "platform"},
		"huntdeck_match":   {"host"},
	}
	byName := map[string]mcpserver.ToolInfo{}
	for _, tool := range catalog {
		byName[tool.Name] = tool
	}
	for tool, args := range known {
		info, ok := byName[tool]
		if !ok {
			t.Fatalf("%s is not in the catalog", tool)
		}
		for _, want := range args {
			found := false
			for _, a := range info.Arguments {
				if a.Name == want && a.Required {
					found = true
				}
			}
			if !found {
				t.Errorf("%s/%s is required but the catalog does not say so", tool, want)
			}
		}
	}
	if !strings.Contains(plain, "required") {
		t.Error("the page marks no argument as required")
	}

	for _, tool := range catalog {
		for _, arg := range tool.Arguments {
			if arg.Required && !strings.Contains(plain, "<code>"+arg.Name+"</code>") {
				t.Errorf("required argument %s of %s is not on the page", arg.Name, tool.Name)
			}
		}
	}
	wantRequired := false
	for _, tool := range catalog {
		for _, arg := range tool.Arguments {
			if arg.Required {
				wantRequired = true
			}
		}
	}
	if wantRequired && !strings.Contains(plain, "required") {
		t.Error("tools have required arguments and the page marks none")
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
