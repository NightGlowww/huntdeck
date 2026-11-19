package web

import (
	"strings"

	"github.com/mdpsec/huntdeck/internal/i18n"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Change sentences and tags in the page's language. The data (asset names,
// old and new values, amounts) is the same; only the words around it change.

// changeTagLang is changeTag for a language ("+ scope" / "+ 范围").
func changeTagLang(lang string, m *i18n.Bundle, kind string) string {
	return m.T(lang, changeTag(kind))
}

// describeChangeLang is describeChange for a language.
func describeChangeLang(lang string, m *i18n.Bundle, c store.ChangeView) string {
	side := ""
	if c.Field == "out_of_scope" {
		side = m.T(lang, " (out of scope)")
	}
	switch c.Kind {
	case store.ChangeProgramAdded:
		if c.OldValue == "removed" {
			return m.TF(lang, "Program is back (%s)", c.NewValue)
		}
		if c.NewValue == "private" {
			return m.T(lang, "New private program")
		}
		return m.T(lang, "New public program")
	case store.ChangeProgramRemoved:
		return m.T(lang, "Program no longer listed")
	case store.ChangeVisibilityChanged:
		return m.TF(lang, "Visibility: %s → %s", m.T(lang, c.OldValue), m.T(lang, c.NewValue))
	case store.ChangeStateChanged:
		// The states are enumerable ("open", "paused", "closed"), so they read
		// in the page's language too; anything unknown passes through.
		if c.Flips > 0 {
			return m.TF(lang, "Paused and reopened: %d status changes in a week, the last %s → %s",
				c.Flips, m.T(lang, c.OldValue), m.T(lang, c.NewValue))
		}
		return m.TF(lang, "Status: %s → %s", m.T(lang, c.OldValue), m.T(lang, c.NewValue))
	case store.ChangeBountyChanged:
		if c.NewValue == "true" {
			return m.T(lang, "Now offers bounties")
		}
		return m.T(lang, "No longer offers bounties")
	case store.ChangeProgramRenamed:
		return m.TF(lang, "Renamed from %s", c.OldValue)
	case store.ChangeScopeAdded:
		if c.OldValue == "removed" {
			return m.TF(lang, "Scope re-added%s: %s", side, assetText(c.NewValue))
		}
		return m.TF(lang, "Scope added%s: %s", side, assetText(c.NewValue))
	case store.ChangeScopeRemoved:
		return m.TF(lang, "Scope removed%s: %s", side, assetText(c.OldValue))
	case store.ChangeScopeUpdated:
		field := m.T(lang, strings.ReplaceAll(c.Field, "_", " "))
		of := ""
		if c.Asset != "" && c.Field != "identifier" {
			of = m.TF(lang, " of %s", assetText(c.Asset))
		}
		if c.Field == "instruction" {
			return m.TF(lang, "Scope instructions changed%s", m.TF(lang, " of %s", assetText(c.Asset)))
		}
		old, cur := c.OldValue, c.NewValue
		if old == "" {
			old = "—"
		}
		if cur == "" {
			cur = "—"
		}
		return m.TF(lang, "Scope %s%s: %s → %s", field, of, old, cur)
	case store.ChangePolicyChanged, store.ChangeRewardsChanged:
		return describeRulesLang(lang, m, c)
	case store.ChangeAnnouncement:
		title, _, _ := strings.Cut(c.NewValue, "\n")
		return m.TF(lang, "Announcement: %s", strings.TrimSpace(title))
	}
	return c.Kind
}

// describeRulesLang is describeRules for a language.
func describeRulesLang(lang string, m *i18n.Bundle, c store.ChangeView) string {
	if c.Kind == store.ChangeRewardsChanged {
		diff := rewardsDiff(c.OldValue, c.NewValue, c.Currency)
		switch len(diff) {
		case 0:
			return m.T(lang, "Bounty table reordered")
		case 1:
			return m.TF(lang, "Bounty table: %s", describeRewardLang(lang, m, diff[0]))
		}
		return m.TF(lang, "Bounty table: %s (+%d more)", describeRewardLang(lang, m, diff[0]), len(diff)-1)
	}
	label := ruleLabels[c.Field]
	if label == "" {
		label = c.Field
	}
	label = m.T(lang, label)
	switch {
	case strings.TrimSpace(c.OldValue) == "":
		return m.TF(lang, "%s added", label)
	case strings.TrimSpace(c.NewValue) == "":
		return m.TF(lang, "%s removed", label)
	}
	_, added, removed := textDiff(c.OldValue, c.NewValue)
	return m.TF(lang, "%s edited (%s, %s)", label, lineCountLang(lang, m, "+", added), lineCountLang(lang, m, "−", removed))
}

func describeRewardLang(lang string, m *i18n.Bundle, c rewardChange) string {
	switch {
	case c.Before == "":
		return m.TF(lang, "new row %s: %s", c.Scope, c.After)
	case c.After == "":
		return m.TF(lang, "row removed %s", c.Scope)
	}
	return m.TF(lang, "%s %s: %s → %s", c.Scope, m.T(lang, c.Severity), c.Before, c.After)
}

func lineCountLang(lang string, m *i18n.Bundle, sign string, n int) string {
	if n == 1 {
		return m.TF(lang, "%s1 line", sign)
	}
	return m.TF(lang, "%s%d lines", sign, n)
}
