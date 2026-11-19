package web

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestResponsiveRulesAreNotOverridden reads the stylesheets in load order and
// fails when a rule inside a media query is re-declared, for the same selector
// and the same property with a different value, by a later file outside any
// query: the later declaration wins at every width, so the responsive one never
// applies. A re-declaration with the same value is redundant, not broken.
//
// Not hypothetical: `table.surface .tv { white-space: normal }` sat in
// 10-responsive.css under max-width: 420px while 12-board.css declared
// `white-space: nowrap` unconditionally. The phone layout never wrapped a long
// URL; it spilled out of its cell and the panel clipped it, for weeks.
func TestResponsiveRulesAreNotOverridden(t *testing.T) {
	files, err := filepath.Glob("static/css/*.css")
	if err != nil || len(files) == 0 {
		t.Fatalf("no stylesheets: %v", err)
	}
	sort.Strings(files) // the bundle joins them in name order

	var rules []cssRule
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		rules = append(rules, parseCSS(string(b), filepath.Base(f))...)
	}
	if len(rules) < 100 {
		t.Fatalf("only %d rules parsed: the reader is not reading the stylesheets", len(rules))
	}

	for i, r := range rules {
		if r.media == "" {
			continue
		}
		for _, later := range rules[i+1:] {
			if later.sel != r.sel || later.media != "" {
				continue
			}
			for prop, want := range r.props {
				got, ok := later.props[prop]
				if !ok || got == want {
					continue
				}
				t.Errorf("%q: %s sets %s: %s inside %q, but %s sets %s: %s unconditionally",
					r.sel, r.file, prop, want, r.media, later.file, prop, got)
			}
		}
	}
}
