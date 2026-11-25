package config

import (
	"sort"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/i18n"
)

// TestEditableFieldsAreTranslated guards the words Settings prints for each
// field: the label above the box and the help under it come from config, not
// from a template, so the template coverage test never sees them. Twenty-two of
// the forty-six were English under Chinese headings - the POJIA, scan handoff
// and decision model boxes read as half-translated panels.
//
// Empty help is allowed: not every field has one.
func TestEditableFieldsAreTranslated(t *testing.T) {
	b, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for _, f := range Editable {
		if !b.HasMsg("zh", f.Label) {
			missing = append(missing, "label: "+f.Label)
		}
		if f.Help != "" && !b.HasMsg("zh", f.Help) {
			missing = append(missing, "help: "+f.Help)
		}
	}
	if len(Editable) < 30 {
		t.Fatalf("only %d editable fields: the list stopped loading", len(Editable))
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d field(s) Settings prints have no zh translation:\n%s", len(missing), strings.Join(missing, "\n"))
	}
}
