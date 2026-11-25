package web

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/i18n"
)

// TestQueueReasonsAreTranslated pins the queue's why-column: the reasons a row
// carries are English sentences built in huntqueue, rewritten for Chinese by a
// shape in reason.go. A reason with no shape reads English in a Chinese page,
// and nothing noticed - the four handoff reasons did exactly that.
//
// Only the reasons with no arguments are checked here: one built with %s has
// its values spliced in before the shape sees it, so its source literal is not
// the text the shape matches.
func TestQueueReasonsAreTranslated(t *testing.T) {
	messages, err := i18n.Load()
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
		if reasonText("zh", messages, m[1]) == m[1] {
			missing = append(missing, m[1])
		}
	}
	if seen < 4 {
		t.Fatalf("only %d static reasons found: the pattern stopped matching", seen)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d reason(s) have no Chinese shape:\n%s", len(missing), strings.Join(missing, "\n"))
	}
}
