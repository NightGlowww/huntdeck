package web

import (
	"os"
	"regexp"
	"testing"
)

// The methodology page is written twice, once per language, and both copies are
// outside the i18n linter's reach by design. That is how the Chinese page came
// to be missing the HuntQueue section the English one gained: nothing compared
// them. The section ids are the shape of the page, so they are what is compared.
func TestMethodologyPagesHaveTheSameSections(t *testing.T) {
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
	en := sections("templates/methodology.html")
	zh := sections("templates/methodology.zh.html")
	if len(en) < 10 {
		t.Fatalf("only %d sections found in the English page: is the parser looking?", len(en))
	}
	for id := range en {
		if !zh[id] {
			t.Errorf("the Chinese methodology page has no section %q", id)
		}
	}
	for id := range zh {
		if !en[id] {
			t.Errorf("the English methodology page has no section %q", id)
		}
	}
}
