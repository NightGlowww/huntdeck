package web

import (
	"strings"
	"testing"
)

// TestOnePerProgramReducesThePage pins the switch end to end, after two rounds of
// it doing nothing: the rows come back through a memo, and its key did not include
// `one`, so the filtered view was served the plain list from the cache. Every test
// of the filter passed; the page did not change.
func TestOnePerProgramReducesThePage(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) {})
	count := func(path string) int {
		t.Helper()
		_, body := e.do(e.client(), "GET", path, nil, nil)
		i, j := strings.Index(body, "<tbody"), strings.Index(body, "</tbody>")
		if i < 0 || j < i {
			t.Fatalf("%s: no table", path)
		}
		return strings.Count(body[i:j], "<tr")
	}
	plain := count("/targets?kind=all")
	one := count("/targets?kind=all&one=1")
	if plain < 2 {
		t.Fatalf("the fixture has %d rows; nothing to deduplicate", plain)
	}
	if one >= plain {
		t.Errorf("one-per-program did not reduce the page: %d rows, plain %d", one, plain)
	}
}
