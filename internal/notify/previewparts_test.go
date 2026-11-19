package notify

import (
	"strconv"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// A message longer than the channel's limit is split, and the preview says how
// many parts it would take.
func TestPreviewTextSplits(t *testing.T) {
	at := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	events := make([]Event, 0, 60)
	for i := 0; i < 60; i++ {
		events = append(events, Event{Kind: EventScopeAdded, At: at, Platform: "fake",
			Program: &Program{Platform: "fake", Handle: "acme" + strconv.Itoa(i), Name: "Acme " + strconv.Itoa(i)},
			Assets:  []Asset{{Identifier: "host" + strconv.Itoa(i) + ".example.com", Type: "domain", InScope: true}},
		})
	}
	text, parts := PreviewText(store.NotifyBark, events, at, "", time.UTC)
	if parts < 2 {
		t.Fatalf("60 events fit one Bark message (%d parts, %d bytes)", parts, len(text))
	}
	if text == "" {
		t.Error("no text")
	}
}
