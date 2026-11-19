package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"testing"
)

// TestEveryCommandIsDocumented keeps docs/cli.md honest in both directions: a
// command nobody wrote down is one nobody will run, and a documented command
// that no longer exists is worse — it reads as supported. The API and the MCP
// tools have had this guard for a while; the CLI was the surface without one.
func TestEveryCommandIsDocumented(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "cli.md"))
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, m := range regexp.MustCompile("`huntdeck ([a-z-]+)").FindAllStringSubmatch(string(doc), -1) {
		documented[m[1]] = true
	}
	if len(documented) < 5 {
		t.Fatalf("only %d commands read from docs/cli.md", len(documented))
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var commands []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, el := range lit.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			k, ok := kv.Key.(*ast.BasicLit)
			if !ok || k.Kind != token.STRING {
				continue
			}
			name, err := strconv.Unquote(k.Value)
			if err != nil || !regexp.MustCompile(`^[a-z][a-z-]*$`).MatchString(name) {
				continue
			}
			commands = append(commands, name)
		}
		return true
	})
	if len(commands) < 5 {
		t.Fatalf("only %d commands read from main.go", len(commands))
	}

	for _, c := range commands {
		if !documented[c] {
			t.Errorf("%q is a command but docs/cli.md does not mention it", c)
		}
		delete(documented, c)
	}
	left := make([]string, 0, len(documented))
	for name := range documented {
		left = append(left, name)
	}
	slices.Sort(left)
	for _, name := range left {
		t.Errorf("docs/cli.md documents %q, which is not a command", name)
	}
}
