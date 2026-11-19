package web

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/config"
)

// The model has its own page: its settings live there, and the Accounts group
// list no longer carries them. The list is asserted directly because the
// Accounts tab is hidden where saving is off - a page assertion there would
// pass on an empty response, which is the guard that cannot fail.
func TestModelHasItsOwnPage(t *testing.T) {
	if slices.Contains(accountGroups, config.GroupAI) {
		t.Fatal("the model is still an Accounts group: its fields would render in two places")
	}
	e := newTestEnv(t)
	res, body := e.do(e.client(), http.MethodGet, "/llm", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/llm: %d", res.StatusCode)
	}
	for _, want := range []string{"HUNTDECK_AI_BASE_URL", "HUNTDECK_AI_MODEL", "HUNTDECK_AI_KEY", "/llm/test"} {
		if !strings.Contains(body, want) {
			t.Errorf("/llm is missing %q", want)
		}
	}
}
