package web

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/i18n"
)

// TestEveryQueueChipLabelIsTranslated pins the chip labels against the catalog:
// the template used to name each fixed chip by hand in an if-chain, so a chip
// added later fell through to the platform label and printed in English - the two
// scanned chips did exactly that with their translations sitting unused.
//
// The labels are read from the code that builds them, not from the list that
// decides how to render them: a test that read the render list would agree with
// whatever that list forgot.
func TestEveryQueueChipLabelIsTranslated(t *testing.T) {
	m, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile("queuepage.go")
	if err != nil {
		t.Fatal(err)
	}
	lit := regexp.MustCompile(`Label: "([^"]+)"`)
	seen := 0
	var missing []string
	for _, g := range lit.FindAllStringSubmatch(string(body), -1) {
		label := g[1]
		// Every fixed label is copy: the platform chips take their label from the
		// platforms list, not from a literal here. One that is not in the list is
		// the bug this test exists for, so it fails rather than being skipped.
		seen++
		if !isQueueChipLabel(label) {
			missing = append(missing, label+" (not in queueChipLabels, so it renders raw)")
			continue
		}
		if !m.HasMsg("zh", label) {
			missing = append(missing, label+" (no zh entry)")
		}
	}
	if seen < 5 {
		t.Fatalf("only %d fixed chip labels found: the list or the pattern stopped matching", seen)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("chip label(s) with no zh entry:\n%s", strings.Join(missing, "\n"))
	}
	// And the list must not name a label no chip uses: a stale entry hides the
	// next chip's absence from it.
	for label := range queueChipLabels {
		if !strings.Contains(string(body), `Label: "`+label+`"`) {
			t.Errorf("queueChipLabels names %q, which no chip builds", label)
		}
	}
}
