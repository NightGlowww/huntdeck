// Package i18n holds the interface's translations.
//
// Messages are keyed by their English source text, gettext-style: a template
// writes {{T "Hunt board"}} and a catalog maps that exact sentence to the
// translated one. A message a catalog doesn't have falls back to the English,
// so a page never shows a key, and untranslated pages keep working while a
// translation catches up.
//
// A catalog is one JSON file per language under catalog/, named after the
// language ("zh.json"), with the language's own name and its messages:
//
//	{"name": "中文", "messages": {"Hunt board": "狩猎面板"}}
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed catalog/*.json
var catalogFS embed.FS

// The languages the interface ships with. English is the source language:
// its catalog may hold overrides, but a missing entry reads as the message
// itself.
const (
	English = "en"
	Chinese = "zh"
)

// Bundle is the loaded catalogs.
type Bundle struct {
	langs []string                     // every language with a catalog, sorted
	names map[string]string            // language -> its own name
	msgs  map[string]map[string]string // language -> message -> translation
}

type catalog struct {
	Name     string            `json:"name"`
	Messages map[string]string `json:"messages"`
}

// Load reads the catalogs compiled into the binary.
func Load() (*Bundle, error) { return New(catalogFS, "catalog") }

// New reads a directory of catalogs from fsys: one <lang>.json per language.
func New(fsys fs.FS, dir string) (*Bundle, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("i18n: read %s: %w", dir, err)
	}
	b := &Bundle{names: map[string]string{}, msgs: map[string]map[string]string{}}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		lang := strings.TrimSuffix(e.Name(), ".json")
		raw, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("i18n: read %s: %w", e.Name(), err)
		}
		var c catalog
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("i18n: %s: %w", e.Name(), err)
		}
		name := c.Name
		if name == "" {
			name = lang
		}
		b.names[lang] = name
		b.msgs[lang] = c.Messages
		b.langs = append(b.langs, lang)
	}
	sort.Strings(b.langs)
	return b, nil
}

// Langs lists the languages with a catalog, sorted.
func (b *Bundle) Langs() []string {
	if b == nil {
		return nil
	}
	return append([]string(nil), b.langs...)
}

// Has reports whether lang has a catalog.
func (b *Bundle) Has(lang string) bool {
	if b == nil {
		return false
	}
	_, ok := b.msgs[Normalize(lang)]
	return ok
}

// Name is a language's own name ("中文", "English"), or the tag itself. The
// source language has no catalog of its own, so it carries its name here.
func (b *Bundle) Name(lang string) string {
	if b == nil {
		return lang
	}
	if n := b.names[Normalize(lang)]; n != "" {
		return n
	}
	if Normalize(lang) == English {
		return "English"
	}
	return lang
}

// T translates msg into lang. A language without a catalog, or a message
// without a translation, returns msg unchanged.
func (b *Bundle) T(lang, msg string) string {
	if b == nil || msg == "" {
		return msg
	}
	if m := b.msgs[Normalize(lang)]; m != nil {
		if s, ok := m[msg]; ok && s != "" {
			return s
		}
	}
	return msg
}

// TF translates a format string and fills in its arguments.
func (b *Bundle) TF(lang, msg string, args ...any) string {
	return fmt.Sprintf(b.T(lang, msg), args...)
}

// Normalize reduces a tag to its language: "zh-CN" and "zh_Hans" are "zh",
// "en-US" is "en".
func Normalize(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(tag, "-_"); i > 0 {
		tag = tag[:i]
	}
	return tag
}

// Match picks the best language for an Accept-Language header, from langs.
// It honours q-values, matches "zh-CN,zh;q=0.9" to "zh", and returns "" when
// none of langs fits (the caller then uses its default).
func Match(header string, langs []string) string {
	if header == "" || len(langs) == 0 {
		return ""
	}
	have := map[string]string{} // normalized language -> catalog language
	for _, l := range langs {
		have[Normalize(l)] = l
	}
	type pick struct {
		lang string
		q    float64
		pos  int
	}
	var picks []pick
	for i, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tag, q := part, 1.0
		if j := strings.Index(part, ";"); j >= 0 {
			tag = strings.TrimSpace(part[:j])
			if strings.HasPrefix(strings.TrimSpace(part[j+1:]), "q=") {
				if v, err := parseQ(strings.TrimSpace(part[j+1:])[2:]); err == nil {
					q = v
				}
			}
		}
		if q <= 0 {
			continue
		}
		if l, ok := have[tag]; ok {
			picks = append(picks, pick{l, q, i})
		} else if l, ok := have[Normalize(tag)]; ok {
			picks = append(picks, pick{l, q, i})
		}
	}
	if len(picks) == 0 {
		return ""
	}
	sort.SliceStable(picks, func(i, j int) bool {
		if picks[i].q != picks[j].q {
			return picks[i].q > picks[j].q
		}
		return picks[i].pos < picks[j].pos
	})
	return picks[0].lang
}

func parseQ(s string) (float64, error) {
	var v float64
	_, err := fmt.Sscanf(s, "%f", &v)
	return v, err
}

// HasMsg reports whether lang translates msg at all. A test uses it to keep
// the catalog from lagging behind the templates: T() falls back to the English
// source, so a missing entry shows as English with nothing to notice it.
func (b *Bundle) HasMsg(lang, msg string) bool {
	if b == nil || msg == "" {
		return false
	}
	m := b.msgs[Normalize(lang)]
	if m == nil {
		return false
	}
	s, ok := m[msg]
	return ok && s != ""
}
