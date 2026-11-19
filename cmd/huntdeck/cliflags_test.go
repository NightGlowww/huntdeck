package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The command-name guard cannot see a flag: every command was documented while
// "queue --explain" printed a plain table, because the flag existed and did
// nothing. This walks the flag definitions and checks each one is written down.
func TestEveryFlagIsDocumented(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "cli.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)

	// Flags every command accepts, documented once in the global section.
	global := map[string]bool{"data": true, "help": true, "version": true}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	defined := map[string]bool{}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "String", "Bool", "Int", "Duration", "Int64", "Float64":
			default:
				return true
			}
			recv, ok := sel.X.(*ast.Ident)
			if !ok || recv.Name != "fs" {
				return true
			}
			if len(call.Args) == 0 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if v, err := strconv.Unquote(lit.Value); err == nil && v != "" {
				defined[v] = true
			}
			return true
		})
	}
	if len(defined) < 20 {
		t.Fatalf("only %d flags found: the walker is looking at nothing", len(defined))
	}

	names := make([]string, 0, len(defined))
	for n := range defined {
		if global[n] {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	missing := 0
	for _, n := range names {
		pat := regexp.MustCompile("(^|[^a-z-])-{1,2}" + regexp.QuoteMeta(n) + "([^a-z-]|$)")
		if !pat.MatchString(text) {
			t.Errorf("huntdeck flag --%s is not in docs/cli.md", n)
			missing++
		}
	}
	if missing == 0 {
		t.Logf("checked %d flags", len(names))
	}
}
