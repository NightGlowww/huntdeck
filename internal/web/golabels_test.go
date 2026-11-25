package web

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/i18n"
	"github.com/mdpsec/huntdeck/internal/reports"
)

// TestGoBuiltLabelsAreTranslated guards the words the Go side puts on screen.
//
// Templates go through {{T}}, and the coverage test checks those. Strings the
// handlers pass as data do not: pageData.Title reached the layout raw, so every
// page showed "Reports" over a Chinese layout, and the report labels (Critical,
// Accepted, ...) printed the same way. The check is the catalog, so a new label
// fails here until it is translated.
//
// Comments, URLs and code are skipped: only the Title: "..." values are read,
// because that is the one shape a handler hands the layout.
func TestGoBuiltLabelsAreTranslated(t *testing.T) {
	b, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}

	var missing []string

	// The report vocabulary pages print beside every row.
	for _, s := range reports.States {
		if !b.HasMsg("zh", s.Label) {
			missing = append(missing, "reports.States: "+s.Label)
		}
	}
	for _, s := range reports.Severities {
		if !b.HasMsg("zh", s.Label) {
			missing = append(missing, "reports.Severities: "+s.Label)
		}
	}

	// The sign-in lines' words are keys too: a missing one shows English.
	keyLit := regexp.MustCompile(`(?:TextKey|LabelKey|NameKey):\s*"([^"\\]{1,80})"`)
	title := regexp.MustCompile(`Title:\s*"([^"\\]{1,60})"`)
	seen := 0
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range keyLit.FindAllStringSubmatch(string(body), -1) {
			seen++
			if !b.HasMsg("zh", m[1]) {
				missing = append(missing, name+" key: "+m[1])
			}
		}
		for _, m := range title.FindAllStringSubmatch(string(body), -1) {
			seen++
			if !b.HasMsg("zh", m[1]) {
				missing = append(missing, name+": "+m[1])
			}
		}
	}
	if seen < 10 {
		t.Fatalf("only %d page titles found: the pattern stopped matching", seen)
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d label(s) the Go side prints have no zh translation:\n%s", len(missing), strings.Join(missing, "\n"))
	}
}
