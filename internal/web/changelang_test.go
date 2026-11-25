package web

import (
	"testing"

	"github.com/mdpsec/huntdeck/internal/i18n"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// TestScopeChangeTranslatesItsValues pins the words inside a scope change, not
// just the field's name: "Scope max severity of x: critical → high" was half
// English on a Chinese page, a hundred rows of it, because only the field name
// went through the catalog.
func TestScopeChangeTranslatesItsValues(t *testing.T) {
	m, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		field, old, cur string
		wantIn          []string
	}{
		// The catalog's own words for these: 严重 and 高. What matters is that the
		// platform's English no longer reaches the page.
		{"max_severity", platform.SeverityCritical, platform.SeverityHigh, []string{"严重", "高"}},
		{"asset_type", "url", "wildcard", nil},
	}
	for _, c := range cases {
		c := c
		t.Run(c.field, func(t *testing.T) {
			got := describeChangeLang("zh", m, store.ChangeView{
				Change: store.Change{Kind: store.ChangeScopeUpdated, Field: c.field, OldValue: c.old, NewValue: c.cur},
				Asset:  "example.com",
			})
			for _, w := range c.wantIn {
				if !contains(got, w) {
					t.Errorf("%q does not contain %q", got, w)
				}
			}
		})
	}
	// The English page keeps the platform's own words.
	en := describeChangeLang("en", m, store.ChangeView{
		Change: store.Change{Kind: store.ChangeScopeUpdated, Field: "max_severity", OldValue: platform.SeverityCritical, NewValue: platform.SeverityHigh},
	})
	if !contains(en, platform.SeverityCritical) {
		t.Errorf("the English page lost the platform's word: %q", en)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
