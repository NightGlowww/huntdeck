package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// The settings preview shows Bark and ServerChan the message as it will
// arrive, not as generic cards.
func TestPreviewText(t *testing.T) {
	at := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	e := Event{Kind: EventScopeAdded, At: at, Platform: "fake",
		Program: &Program{Platform: "fake", Handle: "acme", Name: "Acme"},
		Assets:  []Asset{{Identifier: "*.acme.example", Type: "wildcard", InScope: true}},
	}
	bark, barkParts := PreviewText(store.NotifyBark, []Event{e}, at, "https://hd.example", time.UTC)
	if barkParts < 1 {
		t.Errorf("bark preview parts = %d", barkParts)
	}
	for _, want := range []string{"new in-scope", "*.acme.example", "https://hd.example"} {
		if !strings.Contains(bark, want) {
			t.Errorf("bark preview lacks %q:\n%s", want, bark)
		}
	}
	sc, _ := PreviewText(store.NotifyServerChan, []Event{e}, at, "", time.UTC)
	if !strings.Contains(sc, "*.acme.example") {
		t.Errorf("serverchan preview = %q", sc)
	}
	if strings.Contains(bark, "<b>") || strings.Contains(bark, "<a href") {
		t.Errorf("bark preview carries markup: %s", bark)
	}
	if got, n := PreviewText(store.NotifyDiscord, []Event{e}, at, "", time.UTC); got != "" || n != 0 {
		t.Errorf("discord has cards, not a text preview: %q", got)
	}
	if got, n := PreviewText(store.NotifyBark, nil, at, "", time.UTC); got != "" || n != 0 {
		t.Errorf("no events = %q", got)
	}
}

// Bark shows plain text: a link keeps its address after the title, and no
// markup survives.
func TestPlainStyle(t *testing.T) {
	st := plainStyle{}
	if got := st.link("**Acme**", "https://hd.example/programs/fake/acme"); got != "**Acme** (https://hd.example/programs/fake/acme)" {
		t.Errorf("link = %q", got)
	}
	if got := st.link("Acme", ""); got != "Acme" {
		t.Errorf("link without an address = %q", got)
	}
	if got := st.bold("x") + st.code("y") + st.esc("<b>"); got != "xy<b>" {
		t.Errorf("markup = %q", got)
	}
}
