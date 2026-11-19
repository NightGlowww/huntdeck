package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A long page may be written out in full per language rather than a sentence at
// a time (templates/<page>.<lang>.html). Those copies are outside the i18n
// linter by design, which is how the Chinese methodology page came to be
// missing the HuntQueue section the English one gained: nothing compared them.
//
// The guard walks the mechanism, not one instance: every translated page is
// compared with its source, by section id, so the next page to get a
// translation is covered without anyone remembering to add a test.
func TestTranslatedPagesHaveTheSameSections(t *testing.T) {
	id := regexp.MustCompile(`id="([a-z0-9-]+)"`)
	sections := func(path string) map[string]bool {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, m := range id.FindAllStringSubmatch(string(b), -1) {
			out[m[1]] = true
		}
		return out
	}
	translated, err := filepath.Glob("templates/*.zh.html")
	if err != nil || len(translated) == 0 {
		t.Fatalf("no translated pages found (%v): the guard is looking at nothing", err)
	}
	compared := 0
	for _, zhPath := range translated {
		source := strings.TrimSuffix(zhPath, ".zh.html") + ".html"
		if _, err := os.Stat(source); err != nil {
			t.Errorf("%s has no source page", zhPath)
			continue
		}
		en, zh := sections(source), sections(zhPath)
		if len(en) == 0 && len(zh) == 0 {
			// A page with no section ids has nothing to compare; the count below
			// makes sure at least one page does, so the parser cannot be blind.
			continue
		}
		compared++
		for section := range en {
			if !zh[section] {
				t.Errorf("%s is missing section %q", zhPath, section)
			}
		}
		for section := range zh {
			if !en[section] {
				t.Errorf("%s has section %q that its source does not", zhPath, section)
			}
		}
		t.Logf("%s: %d sections, in step with %s", zhPath, len(zh), source)
	}
	if compared == 0 {
		t.Fatal("no translated page had section ids: the guard is looking at nothing")
	}
}
