package mcpserver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// A tool asks the API for its data. Most calls go to /api/…; two ask for the
// plain-text export a page renders, which is a page URL, not an endpoint. Those
// are listed here with their reason so a new one cannot slip in unnoticed.
func TestToolPathsAreEndpointsOrNamedPages(t *testing.T) {
	pages := map[string]string{
		"/changes":   "the plain-text change list (format=txt)",
		"/programs/": "a program's scope as plain text (tab=scope&format=txt)",
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "tools.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "get" {
			return true
		}
		// The second argument is a literal, or a concatenation that starts
		// with one ("/programs/" + handle).
		expr := call.Args[1]
		for {
			if b, ok := expr.(*ast.BinaryExpr); ok && b.Op == token.ADD {
				expr = b.X
				continue
			}
			break
		}
		lit, ok := expr.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		path, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		seen[path] = true
		return true
	})
	if len(seen) < 5 {
		t.Fatalf("only %d paths found in tools.go: the guard is not reading it", len(seen))
	}
	for path := range seen {
		if strings.HasPrefix(path, "/api/") {
			continue
		}
		if _, ok := pages[path]; !ok {
			t.Errorf("tool calls %q, which is neither an /api/ endpoint nor a page listed here", path)
		}
	}
	for path := range pages {
		if !seen[path] {
			t.Errorf("the page %q is listed but no tool calls it any more", path)
		}
	}
}
