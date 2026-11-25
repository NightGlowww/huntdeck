package web

import (
	"testing"

	"github.com/mdpsec/huntdeck/internal/store"
)

// TestRequirementTitlesAreTranslated pins the requirement chips' tooltips:
// each is built as an English sentence in requirements.go and rewritten for
// Chinese by a shape in reason.go. A requirement with no shape reads English
// in a Chinese page and nothing else notices - the template prints its title
// through reqTitle, and the catalog never sees it.
//
// Every flag on, one at a time, with a platform whose label is a plain name.
func TestRequirementTitlesAreTranslated(t *testing.T) {
	signal := 3.0
	cases := []struct {
		name string
		req  store.ProgramRequirements
	}{
		{"min signal", store.ProgramRequirements{MinSignal: &signal}},
		{"id verified", store.ProgramRequirements{IDVerified: true}},
		{"cleared", store.ProgramRequirements{Cleared: true}},
		{"eea", store.ProgramRequirements{EEA: true}},
		{"application", store.ProgramRequirements{Application: true}},
		{"terms", store.ProgramRequirements{Terms: true}},
		{"two factor", store.ProgramRequirements{TwoFactor: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chips := requires("hackerone", c.req)
			if len(chips) != 1 {
				t.Fatalf("%d chips, want 1", len(chips))
			}
			zh := reqTitle("zh", chips[0].Title)
			if zh == chips[0].Title {
				t.Errorf("no Chinese shape for %q: add one to reqTitleShapes", chips[0].Title)
			}
		})
	}
	// The test-credentials note has no platform: covered by notes().
	if zh := reqTitle("zh", "Test credentials available from the platform"); zh == "Test credentials available from the platform" {
		t.Error("the credentials note has no Chinese shape")
	}
}
