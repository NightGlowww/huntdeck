package web

import (
	"fmt"
	"time"

	"github.com/mdpsec/huntdeck/internal/i18n"
)

// Relative times in the page's language. The words are catalog messages
// ("%dm ago" / "%d 分钟前"), so a language supplies its own forms; the
// English strings here are the source.

// agoLang is ago for a language.
func agoLang(lang string, m *i18n.Bundle, t time.Time) string {
	if t.IsZero() {
		return m.T(lang, "never")
	}
	d := time.Since(t)
	if d < 0 || d < time.Minute {
		return m.T(lang, "just now")
	}
	switch {
	case d < time.Hour:
		return m.TF(lang, "%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return m.TF(lang, "%dh ago", int(d.Hours()))
	default:
		return m.TF(lang, "%dd ago", int(d.Hours()/24))
	}
}

func agoPtrLang(lang string, m *i18n.Bundle, t *time.Time) string {
	if t == nil {
		return m.T(lang, "never")
	}
	return agoLang(lang, m, *t)
}

// untilLang is until for a language: "in 52m" / "52 分钟后".
func untilLang(lang string, m *i18n.Bundle, t time.Time) string {
	d := time.Until(t)
	if d <= 0 {
		return m.T(lang, "now")
	}
	var span string
	switch {
	case d < time.Hour:
		span = fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		span = fmt.Sprintf("%dh", int(d.Hours()))
	default:
		span = fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	return m.TF(lang, "in %s", span)
}
