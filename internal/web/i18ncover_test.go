package web

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/i18n"
)

// Every string a template asks the catalog for exists in it. Without this,
// a missing entry is invisible: T() falls back to the English source, so the
// page simply shows English and no test notices.
func TestTemplatesAreTranslated(t *testing.T) {
	b, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	pat := regexp.MustCompile(`\{\{-?\s*TF?\s+"([^"]*)"`)
	var missing []string
	err = fs.WalkDir(templateFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		body, err := fs.ReadFile(templateFS, path)
		if err != nil {
			return err
		}
		for _, m := range pat.FindAllStringSubmatch(string(body), -1) {
			if !b.HasMsg("zh", m[1]) {
				missing = append(missing, path+": "+m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d template string(s) have no zh translation:\n%s", len(missing), strings.Join(missing, "\n"))
	}
}
