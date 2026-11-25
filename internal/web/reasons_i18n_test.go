package web

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/i18n"
)

// TestQueueReasonsAreTranslated pins the queue's why-column, where the reader
// decides what to hunt. A reason is built in huntqueue as an English sentence
// and reaches the page through the catalog (queueViews passes s.T); one built
// with %s has its values spliced in first, so only the static ones can be
// checked from their source literal.
//
// This is the path that matters: an earlier version of this test checked the
// shape table in reason.go instead, which static reasons never pass through -
// it reported four reasons as untranslated that the catalog had carried all
// along, and would have passed four that only looked fixed.
func TestQueueReasonsAreTranslated(t *testing.T) {
	bundle, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile("../huntqueue/huntqueue.go")
	if err != nil {
		t.Fatal(err)
	}
	lit := regexp.MustCompile(`Reason\{Key: "([^"%]{1,80})"\}`)
	seen := 0
	var missing []string
	for _, m := range lit.FindAllStringSubmatch(string(body), -1) {
		seen++
		if !bundle.HasMsg("zh", m[1]) {
			missing = append(missing, m[1])
		}
	}
	if seen < 8 {
		t.Fatalf("only %d static reasons found: the pattern stopped matching", seen)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d reason(s) have no zh entry:\n%s", len(missing), strings.Join(missing, "\n"))
	}
}
