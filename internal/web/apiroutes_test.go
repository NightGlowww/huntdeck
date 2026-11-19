package web

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// apiRoutes is the one place an API endpoint is declared: the /api page, the
// OpenAPI spec and every route guard read it. A route registered on the mux
// directly is invisible to all three — that is how the token endpoints shipped
// without appearing anywhere. This reads the source and refuses the pattern.
func TestAPIRoutesAreDeclaredOnce(t *testing.T) {
	// Not endpoints, and each for a reason.
	exempt := map[string]string{
		"GET /api": "the API page itself, not an endpoint",
		"/api/":    "the fallback that answers unknown API paths in JSON",
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "server.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		pattern, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		if _, ok := exempt[pattern]; ok {
			return true
		}
		path := strings.TrimPrefix(strings.TrimPrefix(pattern, "GET "), "POST ")
		if strings.HasPrefix(path, "/api/") || path == "/api" {
			t.Errorf("line %d registers %q on the mux: put it in apiRoutes", fset.Position(call.Pos()).Line, pattern)
		}
		return true
	})
}
