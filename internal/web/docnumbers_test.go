package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A doc that quotes a number the product changes daily goes stale silently:
// "2 of 8 snapshots" was still in two files a week after it became 3. Write N.
func TestDocsDoNotQuoteTheSnapshotCount(t *testing.T) {
	volatile := regexp.MustCompile(`\d+ of 8 snapshots`)
	files := []string{"../../README.md", "../../AGENTS.md"}
	// Reference docs only: the audit files record what was true on a day,
	// and a before/after table there is meant to quote the old number.
	dirs := []string{"../../docs"}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".md") {
				files = append(files, filepath.Join(dir, e.Name()))
			}
		}
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if m := volatile.FindString(string(raw)); m != "" {
			t.Errorf("%s quotes %q: write N of 8, the number changes daily", f, m)
		}
	}
}
