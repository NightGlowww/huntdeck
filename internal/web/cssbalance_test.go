package web

import (
	"io/fs"
	"strings"
	"testing"
)

// TestStylesheetsBalance guards the whole interface against one stray brace.
//
// The stylesheets are served as one bundle in name order, and a block left
// open in an early file swallows every rule after it: the browser drops them
// without an error. That shipped once - a merge left a truncated copy of the
// input rule in 09-forms.css, and the workbench, the report charts and the
// mail pages all rendered unstyled while every Go test stayed green.
//
// Comments and quoted strings are skipped: a brace inside either is text.
func TestStylesheetsBalance(t *testing.T) {
	files, err := fs.Glob(staticFS, "static/css/*.css")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 30 {
		t.Fatalf("only %d stylesheets found: the pattern stopped matching", len(files))
	}
	for _, name := range files {
		b, err := fs.ReadFile(staticFS, name)
		if err != nil {
			t.Fatal(err)
		}
		depth, line := cssDepth(string(b))
		if depth != 0 {
			t.Errorf("%s: %d block(s) left open at line %d: every rule after it is dropped", name, depth, line)
		}
	}
}

// cssDepth walks a stylesheet, skipping comments and strings, and reports the
// depth left at the end (0 when balanced) and the line the walk ended on. A
// closing brace with nothing open stops the walk: that is a stray one.
func cssDepth(s string) (int, int) {
	depth, line := 0, 1
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\n':
			line++
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			j := strings.Index(s[i+2:], "*/")
			if j < 0 {
				return depth + 1, line // an unterminated comment eats the rest
			}
			line += strings.Count(s[i:i+2+j+2], "\n")
			i += 2 + j + 1
		case c == '"' || c == '\'':
			q := c
			for i++; i < len(s) && s[i] != q; i++ {
				if s[i] == '\\' {
					i++
				}
				if s[i] == '\n' {
					line++
				}
			}
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth < 0 {
				return depth, line
			}
		}
	}
	return depth, line
}
