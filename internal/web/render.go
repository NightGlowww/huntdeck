package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/huntscore"
	"github.com/mdpsec/huntdeck/internal/i18n"
	"github.com/mdpsec/huntdeck/internal/scheduler"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Pages are templates/*.html, each parsed with layout.html and the shared
// pieces in templates/partials.
//
//go:embed templates/*.html templates/partials/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

var funcs = template.FuncMap{
	// huntMean is what the parts weigh, before any scaling: a no-bounty
	// program's score is 75% of it, and the panel says so.
	"huntMean": huntMean,
	// softBreak lets a long URL wrap at its own punctuation instead of
	// anywhere: a phone shows "cybersource.c / om" without it.
	"softBreak": softBreak,
	"ago":       ago,
	"until":     until,
	"agoPtr":    func(t *time.Time) string { return agoPtr(t) },
	// day shows a UTC day bucket (daily snapshots, score history) as is.
	"day": func(t time.Time) string { return fmtTime(t, time.UTC, "2006-01-02") },
	// Money shows compact ("$8.3k"); the *Full funcs give it to the unit
	// ("$8,250") and belong in tooltips only, never for an estimate
	// (TestFullAmountsOnlyInTooltips).
	"money":     humanize.Money,
	"moneyFull": humanize.MoneyFull,
	// days and hours show a duration compact ("132d", "7d 18h").
	"days": func(v *float64) string {
		if v == nil {
			return ""
		}
		return humanize.Days(*v)
	},
	"num":         num,
	"change":      describeChange,
	"changeIcon":  changeIcon,
	"changeClass": changeClass,
	"shortHandle": shortHandle,
	"duration":    func(d time.Duration) string { return humanDuration(d) },
	"interval":    scheduler.FormatInterval,
	// amount, amountRange, count and reward return "" for missing values
	// so templates can show a placeholder with {{with}}.
	"amount": func(v *float64, currency string) string {
		if v == nil {
			return ""
		}
		return humanize.Money(*v, currency)
	},
	"amountFull": func(v *float64, currency string) string {
		if v == nil {
			return ""
		}
		return humanize.MoneyFull(*v, currency)
	},
	"amountRange":     amountRange,
	"amountRangeFull": amountRangeFull,
	"reward":          reward,
	"rewardFull":      rewardFull,
	"palettes":        func() []palette { return palettes },
	"markdown":        markdown,
	"firstLine":       firstLine,
	"website":         website,
	"httpURL":         httpURL,
	"requires":        requires,
	"intv":            func(v *int) int { return deref(v) },
	"boolv":           func(v *bool) bool { return deref(v) },
	"floatv":          func(v *float64) float64 { return deref(v) },
	"hours": func(h *float64) string {
		if h == nil {
			return ""
		}
		return humanize.Hours(*h)
	},
	"perReport": func(paid float64, reports int) float64 {
		if reports == 0 {
			return 0
		}
		return paid / float64(reports)
	},
	"count": func(v *int) string {
		if v == nil {
			return ""
		}
		return thousands(strconv.Itoa(*v))
	},
	"runTook": func(r store.Run) string {
		if r.FinishedAt == nil {
			return "running"
		}
		return humanDuration(r.FinishedAt.Sub(r.StartedAt))
	},
	"plural":     plural,
	"hasFilters": hasFilters,
	"hunters90": func(n *int) string {
		if n == nil {
			return ""
		}
		return hunters90(*n)
	},
	"add":   func(a, b int) int { return a + b },
	"sub":   func(a, b int) int { return a - b },
	"list":  func(v ...string) []string { return v },
	"lower": strings.ToLower,
	"truncate": func(s string, n int) string {
		if r := []rune(s); len(r) > n {
			return string(r[:n-1]) + "…"
		}
		return s
	},
	"acctSection":        acctSection,
	"platformLabel":      platformLabel,
	"platformShort":      platformShort,
	"changeTag":          changeTag,
	"changeSubject":      changeSubject,
	"hasDetail":          hasDetail,
	"huntBand":           huntBand,
	"huntTitle":          huntTitle,
	"huntBars":           huntBars,
	"response":           response,
	"responseKind":       responseKind,
	"assetTypeLabel":     assetTypeLabel,
	"notePreview":        notePreview,
	"huntReason":         huntReason,
	"topBeyondTable":     topBeyondTable,
	"noteRest":           noteRest,
	"assetsTitle":        assetsTitle,
	"avgBounty":          avgBounty,
	"avgBountyTitle":     avgBountyTitle,
	"huntersLabel":       huntersLabel,
	"huntersHint":        huntersHint,
	"countedTitle":       countedTitle,
	"validEstimateTitle": validEstimateTitle,
	"validTitle":         validTitle,
	"validMonthTitle":    validMonthTitle,
	"bountiesTitle":      bountiesTitle,
	"hiddenName":         scope.HiddenName,
	"plink":              plink,
	"paidAllOf":          paidAllOf,
	"notes":              notes,
	"hiddenLabel":        func() string { return scope.HiddenLabel },
	"huntLabel":          func(k string) string { return huntLabels[k] },
	"huntHint":           func(k string) string { return huntHints[k] },
	"huntGate":           func(g string) string { return huntGates[g] },
	"huntPreset":         func(k string) string { return huntscore.FindPreset(k).Label },
	"huntPresets":        func() []huntscore.Preset { return huntscore.Presets },
	"huntWeights":        huntWeights,
	"join":               strings.Join,
	"channelKind":        channelKindLabel,
	"neg":                func(v float64) float64 { return -v },
	"percent":            func(v float64) string { return strconv.FormatFloat(math.Round(v*100), 'f', 0, 64) + "%" },
	"kindLabel":          kindLabel,
	"boardKinds":         func() any { return boardKinds },
	"boardDays":          func() []string { return boardDays },
	"boardNames":         func() int { return boardNames },
	"boardRows":          func() int { return boardRows },
	"boardPickRows":      func() int { return boardPickRows },
	"boardFlips":         func() int { return store.FlipFlops },
	"fresh":              func(t time.Time) bool { return time.Since(t) < freshFor },
	"freshPtr":           func(t *time.Time) bool { return t != nil && time.Since(*t) < freshFor },
	"newEntry":           newEntry,
	"expandsTo":          expandsTo,
	"age":                age,
	"recent":             func(t time.Time) bool { return time.Since(t) < recentFor },
}

// timeFuncs show instants in the zone zone returns.
func timeFuncs(zone func() *time.Location) template.FuncMap {
	return template.FuncMap{
		"date":     func(t time.Time) string { return fmtTime(t, zone(), "2006-01-02") },
		"datetime": func(t time.Time) string { return fmtTime(t, zone(), "2006-01-02 15:04 MST") },
		// iso is a time for scripts (data-at): UTC, RFC 3339.
		"iso": func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
		"datePtr": func(t *time.Time) string {
			if t == nil {
				return "—"
			}
			return fmtTime(*t, zone(), "2006-01-02")
		},
	}
}

// fileExists reports whether a template file is embedded.
func fileExists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}

// loadTemplates parses each page together with the shared layout, with
// times shown in the zone zone returns. It returns one set of templates per
// language: every page is parsed once per language with that language's
// translation function, so rendering picks a set and stays allocation-free.
//
// readOnlyLinks are the links a read-only copy shows its visitors; a full
// install shows none.
type readOnlyLinks struct {
	XAccount string // an X account to follow for news (Deps.XAccount)
	RepoURL  string // HuntDeck's source repository, to run your own (Deps.RepoURL)
}

func loadTemplates(zone func() *time.Location, version string, readOnly bool, links readOnlyLinks, langs []string, messages *i18n.Bundle) (map[string]map[string]*template.Template, error) {
	if !readOnly {
		links = readOnlyLinks{}
	}
	pages, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	// static links a static file with a version that changes whenever any of
	// them does, so browsers don't keep a cached copy from an older build.
	static := template.FuncMap{
		"static": func(name string) string { return "/static/" + name + "?v=" + version },
		// readOnly is true in a read-only copy (readonly.go): pages leave
		// out what can't be used there.
		"readOnly": func() bool { return readOnly },
		// xAccount is the X account to follow for news, if any
		// (Deps.XAccount); a full install shows none.
		"xAccount": func() string { return links.XAccount },
		// repoURL is HuntDeck's source repository, if any (Deps.RepoURL);
		// a full install shows none.
		"repoURL": func() string { return links.RepoURL },
		// repoName is that address as shown: no scheme, e.g.
		// github.com/mdpsec/huntdeck.
		"repoName": func() string { return strings.TrimPrefix(strings.TrimPrefix(links.RepoURL, "https://"), "www.") },
	}
	out := map[string]map[string]*template.Template{}
	for _, lang := range langs {
		localized := template.FuncMap{
			"T":    func(msg string) string { return messages.T(lang, msg) },
			"TF":   func(msg string, args ...any) string { return messages.TF(lang, msg, args...) },
			"Lang": func() string { return lang },
			// plural picks the singular or the plural message and translates
			// it: a language without plural forms holds the same words in both.
			"plural": func(n int, one, many string) string {
				if n == 1 {
					return messages.T(lang, one)
				}
				return messages.T(lang, many)
			},
			// HuntScore's reasons are stored in English when a score is
			// computed; they read in the page's language here.
			"huntReason": func(r string) template.HTML { return reasonHTML(lang, messages, r) },
			// A target's reasons, joined for a tooltip.
			"reasons": func(rs []string) string { return reasonsText(lang, messages, rs) },
			// Requirement chips' tooltips.
			"reqTitle": func(s string) string { return reqTitle(lang, s) },
			// The column tooltips' fixed sentences (programcells.go,
			// statsbounties.go) read in the page's language too.
			"validMonthTitle": func(p string) string { return messages.T(lang, validMonthTitle(p)) },
			"bountiesTitle":   func(p string) string { return messages.T(lang, bountiesTitle(p)) },
			// How a counted figure was worked out, in the page's language.
			"countedTitle": func(p store.ProgramListItem, what string) string {
				return countedTitleLang(lang, messages, p, what)
			},
			// A chart's own labels: "Jan", "Jan 2", "9mo 1w". A duration
			// ("9mo 1w", "132d") reads in the page's language too.
			"fig": func(s string) string { return figText(lang, s) },
			"days": func(v *float64) string {
				if v == nil {
					return ""
				}
				return figText(lang, humanize.Days(*v))
			},
			// Changes read in the page's language too: "+ scope" / "+ 范围",
			// "Scope added: x" / "范围新增：x".
			"change":    func(c store.ChangeView) string { return describeChangeLang(lang, messages, c) },
			"changeTag": func(k string) string { return changeTagLang(lang, messages, k) },
			// Times read in the page's language: "3d ago" / "3 天前".
			"ago":    func(t time.Time) string { return agoLang(lang, messages, t) },
			"agoPtr": func(t *time.Time) string { return agoPtrLang(lang, messages, t) },
			"until":  func(t time.Time) string { return untilLang(lang, messages, t) },
		}
		set := map[string]*template.Template{}
		for _, p := range pages {
			name := strings.TrimSuffix(strings.TrimPrefix(p, "templates/"), ".html")
			if name == "layout" || name == "login" {
				continue
			}
			if strings.Contains(name, ".") { // a page's translation, templates/<page>.<lang>.html
				continue
			}
			// A long page written out in full (Methodology) has a file per
			// language rather than a message per sentence: the translation is
			// the page, with the same markup.
			src := p
			if lang != i18n.English {
				if translated := "templates/" + name + "." + lang + ".html"; fileExists(templateFS, translated) {
					src = translated
				}
			}
			t, err := template.New("layout.html").Funcs(funcs).Funcs(timeFuncs(zone)).Funcs(static).Funcs(localized).
				ParseFS(templateFS, "templates/layout.html", "templates/partials/*.html", src)
			if err != nil {
				return nil, fmt.Errorf("template %s (%s): %w", name, lang, err)
			}
			set[name] = t
		}
		// The login page stands alone: no sidebar, no top bar, no data.
		t, err := template.New("login.html").Funcs(funcs).Funcs(timeFuncs(zone)).Funcs(static).Funcs(localized).
			ParseFS(templateFS, "templates/login.html")
		if err != nil {
			return nil, fmt.Errorf("template login (%s): %w", lang, err)
		}
		set["login"] = t
		out[lang] = set
	}
	return out, nil
}
