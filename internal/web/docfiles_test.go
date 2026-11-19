package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The docs name source files (`internal/web/timefmt.go`, `app.js`, `conditional.go`).
// A file that moves or disappears leaves the reference pointing at nothing, and no
// reader notices until they go looking. A bare name is matched by suffix, since
// the docs often say `app.js` rather than static/app.js.
func TestDocsReferenceRealFiles(t *testing.T) {
	re := regexp.MustCompile("`([a-zA-Z0-9_./-]+\\.(go|sql|js|css|json|html|sh|py|yml))`")
	files := []string{"../../README.md", "../../AGENTS.md"}
	dirs := []string{"../../docs", "../../docs/audit"}
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

	// Every path in the tree, once.
	var tree []string
	err := filepath.WalkDir("../..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "data" || d.Name() == "bin") {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			tree = append(tree, filepath.ToSlash(p))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
			seen[m[1]] = true
		}
	}
	if len(seen) < 5 {
		t.Fatalf("only %d source files named in the docs: the guard is not reading them", len(seen))
	}
	// Only paths that start in this repo's own directories: a bare name or a
	// /data/... path is something the reader supplies, not something we ship.
	owned := func(ref string) bool {
		for _, prefix := range []string{"internal/", "cmd/", "scripts/", "docs/"} {
			if strings.HasPrefix(ref, prefix) {
				return true
			}
		}
		return false
	}
	checked := 0
	for ref := range seen {
		if !owned(ref) {
			continue
		}
		checked++
		found := false
		for _, p := range tree {
			if strings.HasSuffix(p, "/"+ref) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the docs name %s, which is in no file in the tree", ref)
		}
	}
	if checked < 5 {
		t.Fatalf("only %d repository paths were checked: the guard is not reading them", checked)
	}
}
