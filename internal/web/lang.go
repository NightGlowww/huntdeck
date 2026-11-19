package web

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/i18n"
)

// The interface's language.
//
// A visitor picks one with the sidebar's switcher, kept in a cookie. Without
// a choice the install's HUNTDECK_LANGUAGE decides, and "auto" (the default)
// asks the browser through Accept-Language. English is the source language:
// it always has a complete set of templates, and any message without a
// translation reads as its English.
const (
	langCookieName = "hd_lang"
	langCookieLife = 365 * 24 * time.Hour
)

// pageTitleKeys are the page titles that are messages, not data: a title
// naming a program is never looked up in a catalog.
var pageTitleKeys = map[string]bool{
	"Hunt board": true, "Programs": true, "Invites": true, "Stats": true,
	"Targets": true, "Changes": true, "Disclosed": true, "Methodology": true,
	"Settings": true, "Notifications": true, "API": true, "Feedback": true,
	"Match": true, "Search": true, "Sign in": true, "Disclosed reports": true,
	"Scope check": true,
}

// langLink is one language in the switcher.
type langLink struct {
	Code    string // "zh"
	Name    string // its own name, "中文"
	Short   string // for the narrow sidebar's rail: "中", "EN"
	Current bool   // the page's language
}

// languages lists the languages to build template sets for: English (the
// source) plus every language with a catalog.
func (s *server) languages() []string {
	langs := []string{i18n.English}
	for _, l := range s.messages.Langs() {
		if l != i18n.English {
			langs = append(langs, l)
		}
	}
	if d := i18n.Normalize(s.Language); d != "" && d != "auto" && s.messages.Has(d) && d != i18n.English {
		// A forced default is still built, even without a catalog of its own.
		langs = append(langs, d)
	}
	sort.Strings(langs)
	out := langs[:0]
	for i, l := range langs {
		if i > 0 && langs[i-1] == l {
			continue
		}
		out = append(out, l)
	}
	return out
}

// hasLang reports whether lang can be shown: English is the source language
// and always can, whether or not it has a catalog.
func (s *server) hasLang(lang string) bool {
	return lang == i18n.English || s.messages.Has(lang)
}

// langOf is a request's language: the visitor's choice, then
// HUNTDECK_LANGUAGE, then the browser's Accept-Language, then English.
func (s *server) langOf(r *http.Request) string {
	if c, err := r.Cookie(langCookieName); err == nil {
		if l := i18n.Normalize(c.Value); s.hasLang(l) {
			return l
		}
	}
	if d := i18n.Normalize(s.Language); d != "" && d != "auto" && s.hasLang(d) {
		return d
	}
	if l := i18n.Match(r.Header.Get("Accept-Language"), s.languages()); l != "" {
		return l
	}
	return i18n.English
}

// T translates msg for a request, for handlers that render text themselves.
func (s *server) T(r *http.Request, msg string) string {
	return s.messages.T(s.langOf(r), msg)
}

// langLinks lists the switcher's languages, current first.
func (s *server) langLinks(current string) []langLink {
	var out []langLink
	for _, l := range s.languages() {
		out = append(out, langLink{Code: l, Name: s.messages.Name(l), Short: langShort(l), Current: l == current})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Current != out[j].Current {
			return out[i].Current
		}
		return out[i].Code < out[j].Code
	})
	return out
}

// languageSwitch remembers a visitor's language (POST /settings/language)
// and sends them back to the page they were on.
func (s *server) languageSwitch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Bad request.")
		return
	}
	lang := i18n.Normalize(r.PostFormValue("lang"))
	if !s.hasLang(lang) {
		lang = i18n.English
	}
	//nolint:gosec // Secure follows the request scheme, as the session cookie does
	http.SetCookie(w, &http.Cookie{
		Name:     langCookieName,
		Value:    lang,
		Path:     "/",
		MaxAge:   int(langCookieLife / time.Second),
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
	next := safeNext(r.PostFormValue("next"))
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// langShort is a language's two-letter stand-in for the narrow sidebar:
// "中文" is "中", "English" is "EN".
func langShort(lang string) string {
	switch lang {
	case "zh":
		return "中"
	case "en":
		return "EN"
	}
	if r := []rune(lang); len(r) > 0 {
		return strings.ToUpper(string(r[:min(2, len(r))]))
	}
	return lang
}
