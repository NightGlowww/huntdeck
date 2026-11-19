package web

import (
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/i18n"
	"github.com/mdpsec/huntdeck/internal/notify"
	"github.com/mdpsec/huntdeck/internal/store"
)

// The sentences HuntDeck builds in Go (channel summaries, counted figures)
// read in the page's language; the English path must not change.
func TestLocalizedGoText(t *testing.T) {
	m, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}

	all := notify.Rules{}
	for _, k := range notify.EventKinds {
		all.Events = append(all.Events, k.Key)
	}
	none := notify.Rules{Events: []string{}}
	bountyPrivate := notify.Rules{BountyOnly: true, PrivateOnly: true}

	events := []struct {
		name string
		r    notify.Rules
		want string
	}{
		{"nothing", none, "不发送任何事件"},
		{"everything", all, "全部事件"},
	}
	for _, c := range events {
		if got := eventsTextLang("zh", m, c.r); got != c.want {
			t.Errorf("eventsTextLang(%s) = %q, want %q", c.name, got, c.want)
		}
	}
	if got := eventsTextLang("en", m, none); got != "Nothing" {
		t.Errorf("English events text changed: %q", got)
	}

	if got := filtersTextLang("zh", m, bountyPrivate); got != "仅付费 · 仅私有" {
		t.Errorf("filtersTextLang = %q", got)
	}
	if got := filtersTextLang("zh", m, notify.Rules{}); got != "全部项目" {
		t.Errorf("filtersTextLang(all) = %q", got)
	}
	if got := filtersTextLang("zh", m, notify.Rules{MinBounty: 500}); !strings.Contains(got, "最高赏金") {
		t.Errorf("filtersTextLang(min bounty) = %q", got)
	}
	if got := filtersTextLang("en", m, bountyPrivate); got != "paying only · private only" {
		t.Errorf("English filters text changed: %q", got)
	}

	// A counted 90-day figure says how it was worked out, in Chinese.
	days := 12
	p := store.ProgramListItem{CountedDays: &days}
	p.Platform = "hackerone"
	if got := countedTitleLang("zh", m, p, "bounties paid"); !strings.Contains(got, "估算") || !strings.Contains(got, "12 天") {
		t.Errorf("countedTitleLang = %q", got)
	}
	if got := countedTitleLang("en", m, p, "bounties paid"); got != countedTitle(p, "bounties paid") {
		t.Errorf("English counted title changed: %q", got)
	}
}
