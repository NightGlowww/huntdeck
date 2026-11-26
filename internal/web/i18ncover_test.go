package web

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
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
	seen := 0
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
			seen++
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
	// A regex that stopped matching would leave missing empty and pass: the
	// count is what says the guard is still looking at the templates.
	if seen < 400 {
		t.Fatalf("only %d T/TF calls found across the templates: the pattern stopped matching", seen)
	}
	t.Logf("checked %d template strings", seen)
}

// TestNoTemplateSyntaxInsideT guards against Go template syntax ({{...}})
// embedded inside {{T "..."}} or {{TF "..."}} message strings or catalog
// entries. T() looks up the literal string without executing template actions,
// so any {{...}} inside a translation key prints raw syntax to the user.
func TestNoTemplateSyntaxInsideT(t *testing.T) {
	tCallPat := regexp.MustCompile(`^\{\{-?\s*TF?\s+"((?:[^"\\]|\\.)*)"`)
	var bad []string
	seenActions := 0
	err := fs.WalkDir(templateFS, "templates", func(path string, d fs.DirEntry, err error) error {
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
		for _, seg := range splitKeepingActions(string(body)) {
			if !seg.isAction {
				continue
			}
			if m := tCallPat.FindStringSubmatch(seg.text); m != nil {
				seenActions++
				if strings.Contains(m[1], "{{") || strings.Contains(m[1], "}}") {
					bad = append(bad, path+": "+m[1])
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seenActions < 400 {
		t.Fatalf("only %d T/TF calls scanned: pattern stopped matching", seenActions)
	}

	catalogs, err := filepath.Glob(filepath.Join("..", "i18n", "catalog", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogs) == 0 {
		t.Fatal("no i18n catalog files found")
	}
	for _, catPath := range catalogs {
		raw, err := os.ReadFile(catPath)
		if err != nil {
			t.Fatal(err)
		}
		var cat struct {
			Messages map[string]string `json:"messages"`
		}
		if err := json.Unmarshal(raw, &cat); err != nil {
			t.Fatalf("%s: %v", catPath, err)
		}
		for k, v := range cat.Messages {
			if strings.Contains(k, "{{") || strings.Contains(k, "}}") || strings.Contains(v, "{{") || strings.Contains(v, "}}") {
				bad = append(bad, catPath+": "+k)
			}
		}
	}

	if len(bad) > 0 {
		sort.Strings(bad)
		t.Errorf("%d template or catalog entry(ies) contain {{ or }}:\n%s", len(bad), strings.Join(bad, "\n"))
	}
}
