package web

import (
	"strings"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/i18n"
	"github.com/mdpsec/huntdeck/internal/notify"
)

// eventsTextLang is eventsText (notify.go) in the page's language: the
// kinds a channel sends, its defaults or every one.
func eventsTextLang(lang string, m *i18n.Bundle, r notify.Rules) string {
	if lang != "zh" {
		return eventsText(r)
	}
	var kinds []string
	usual := true
	for _, k := range notify.EventKinds {
		if r.Has(k.Key) {
			kinds = append(kinds, m.T(lang, k.Short))
		}
		usual = usual && r.Has(k.Key) == k.Default
	}
	switch {
	case len(kinds) == len(notify.EventKinds):
		return m.T(lang, "Everything")
	case len(kinds) == 0:
		return m.T(lang, "Nothing")
	}
	joined := strings.Join(kinds, "、")
	if usual {
		return m.TF(lang, "The usual: %s", joined)
	}
	return joined
}

// filtersTextLang is filtersText (notify.go) in the page's language.
func filtersTextLang(lang string, m *i18n.Bundle, r notify.Rules) string {
	if lang != "zh" {
		return filtersText(r)
	}
	var out []string
	if len(r.Platforms) > 0 {
		names := make([]string, len(r.Platforms))
		for i, p := range r.Platforms {
			names[i] = platformLabel(p)
		}
		out = append(out, strings.Join(names, "、"))
	}
	if r.BountyOnly {
		out = append(out, m.T(lang, "paying only"))
	}
	if r.PrivateOnly {
		out = append(out, m.T(lang, "private only"))
	}
	if r.MajorScopeOnly {
		out = append(out, m.T(lang, "wildcards and new roots"))
	}
	if r.MinBounty > 0 {
		out = append(out, m.TF(lang, "top bounty %s+", humanize.Money(r.MinBounty, "USD")))
	}
	if len(r.Tags) > 0 {
		out = append(out, m.TF(lang, "tagged %s", strings.Join(r.Tags, "、")))
	}
	if len(r.ExcludeTags) > 0 {
		out = append(out, m.TF(lang, "not tagged %s", strings.Join(r.ExcludeTags, "、")))
	}
	if len(out) == 0 {
		return m.T(lang, "all programs")
	}
	return strings.Join(out, " · ")
}
