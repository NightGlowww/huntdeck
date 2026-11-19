package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Writing docs through a shell heredoc turned apostrophes into three quotes
// more than once ("the endpoint”'s API key"). It reads as a typo to every
// later reader, so it is checked rather than remembered.
func TestDocsHaveNoEscapingSlips(t *testing.T) {
	dirs := []string{"../../docs", "../../docs/audit"}
	files := []string{"../../README.md", "../../AGENTS.md"}
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
		if strings.Contains(string(raw), "'''") {
			t.Errorf("%s has a mangled apostrophe (three quotes)", f)
		}
	}
}
