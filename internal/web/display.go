package web

import (
	"fmt"
	"html/template"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// palette is a colour theme offered in Settings and the Theme menu; static/css/00-theme.css defines it and
// theme.js applies the one chosen in the browser.
// Short is its description in the top bar's Theme menu.
type palette struct{ Key, Name, Desc, Short string }

var palettes = []palette{
	{"midnight", "Midnight", "Navy and lavender. The default.", "Navy and lavender"},
	{"nord", "Nord", "Cool slate grey and frost blue; soft, low glare.", "Slate and frost blue"},
	{"graphite", "Graphite", "Neutral greys; colour only where it means something.", "Neutral greys"},
	{"bluewhite", "Blue White", "White surfaces and calm ocean blue for focused work.", "Ocean blue and white"},
}

func platformLabel(name string) string {
	switch name {
	case "hackerone":
		return "HackerOne"
	case "bugcrowd":
		return "Bugcrowd"
	case "intigriti":
		return "Intigriti"
	case "yeswehack":
		return "YesWeHack"
	case store.Independent:
		return "Independent"
	}
	// A custom source's name, as its configuration gives it: "inspectiv"
	// reads "Inspectiv".
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// platformShort is the abbreviation hunters use for a platform.
func platformShort(name string) string {
	switch name {
	case "hackerone":
		return "H1"
	case "bugcrowd":
		return "BC"
	case "intigriti":
		return "IGT"
	case "yeswehack":
		return "YWH"
	case store.Independent:
		return "IND"
	}
	// A custom source's first three letters: "INS".
	return strings.ToUpper(name[:min(3, len(name))])
}

// kindLabel names a target kind in the plural, for filters and headings.
func kindLabel(kind string) string {
	switch kind {
	case scope.KindWildcard:
		return "Wildcards"
	case scope.KindDomain:
		return "Domains"
	case scope.KindURL:
		return "URLs"
	case scope.KindCIDR:
		return "CIDR ranges"
	case scope.KindIP:
		return "IPs"
	case scope.KindRoot:
		return "Root domains"
	case kindAll:
		return "All kinds"
	}
	if a := assetKindOf(kind); a != nil {
		return a.Label
	}
	return kind
}

// changeTag is the short label shown next to a change in feeds.
func changeTag(kind string) string {
	switch kind {
	case store.ChangeProgramAdded:
		return "new"
	case store.ChangeProgramRemoved:
		return "gone"
	case store.ChangeScopeAdded:
		return "+ scope"
	case store.ChangeScopeRemoved:
		return "− scope"
	case store.ChangeScopeUpdated:
		return "~ scope"
	case store.ChangeBountyChanged:
		return "bounty"
	case store.ChangeStateChanged:
		return "status"
	case store.ChangeVisibilityChanged:
		return "visibility"
	case store.ChangeProgramRenamed:
		return "renamed"
	case store.ChangePolicyChanged:
		return "policy"
	case store.ChangeRewardsChanged:
		return "rewards"
	case store.ChangeAnnouncement:
		return "announcement"
	}
	return kind
}

// changeSubject is the asset a scope change is about, or "" for program
// changes; feeds show it in monospace so it can be copied.
func changeSubject(c store.ChangeView) string {
	switch c.Kind {
	case store.ChangeScopeAdded:
		return c.NewValue
	case store.ChangeScopeRemoved:
		return c.OldValue
	}
	return ""
}

// newEntry reports whether a scope entry appeared after HuntDeck's first
// import of its program, and recently enough to point out.
func newEntry(e store.EntryView, p *store.ProgramDetail) bool {
	if e.AddedAt != nil {
		return e.RemovedAt == nil && time.Since(*e.AddedAt) < freshFor
	}
	return e.FirstSeenAt.Sub(p.FirstSeenAt) > time.Hour && time.Since(e.FirstSeenAt) < freshFor
}

func changeIcon(kind string) string {
	switch kind {
	case store.ChangeProgramAdded, store.ChangeScopeAdded:
		return "+"
	case store.ChangeProgramRemoved, store.ChangeScopeRemoved:
		return "−"
	default:
		return "~"
	}
}

// changeClass is the CSS class for a change's icon.
func changeClass(kind string) string {
	switch changeIcon(kind) {
	case "+":
		return "add"
	case "−":
		return "del"
	default:
		return "mod"
	}
}

var uuidLike = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// shortHandle hides handles that are opaque IDs (Intigriti uses UUIDs), which
// add nothing next to the program name.
func shortHandle(h string) string {
	if uuidLike.MatchString(h) {
		return ""
	}
	return h
}

// assetText is an asset's identifier as text, or what it is where the
// platform hid its name.
func assetText(identifier string) string {
	if scope.HiddenName(identifier) {
		return "(" + scope.HiddenLabel + ")"
	}
	return identifier
}

// describeChange renders a change as a short sentence.
func describeChange(c store.ChangeView) string {
	side := func() string {
		if c.Field == "out_of_scope" {
			return " (out of scope)"
		}
		return ""
	}
	switch c.Kind {
	case store.ChangeProgramAdded:
		if c.OldValue == "removed" {
			return "Program is back (" + c.NewValue + ")"
		}
		if c.NewValue == "private" {
			return "New private program"
		}
		return "New public program"
	case store.ChangeProgramRemoved:
		return "Program no longer listed"
	case store.ChangeVisibilityChanged:
		return "Visibility: " + c.OldValue + " → " + c.NewValue
	case store.ChangeStateChanged:
		if c.Flips > 0 {
			return fmt.Sprintf("Paused and reopened: %d status changes in a week, the last %s → %s", c.Flips, c.OldValue, c.NewValue)
		}
		return "Status: " + c.OldValue + " → " + c.NewValue
	case store.ChangeBountyChanged:
		if c.NewValue == "true" {
			return "Now offers bounties"
		}
		return "No longer offers bounties"
	case store.ChangeProgramRenamed:
		return "Renamed from " + c.OldValue
	case store.ChangeScopeAdded:
		if c.OldValue == "removed" {
			return "Scope re-added" + side() + ": " + assetText(c.NewValue)
		}
		return "Scope added" + side() + ": " + assetText(c.NewValue)
	case store.ChangeScopeRemoved:
		return "Scope removed" + side() + ": " + assetText(c.OldValue)
	case store.ChangeScopeUpdated:
		field := strings.ReplaceAll(c.Field, "_", " ")
		of := "" // the asset, where the change doesn't name it
		if c.Asset != "" && c.Field != "identifier" {
			of = " of " + assetText(c.Asset)
		}
		if c.Field == "instruction" {
			return "Scope instructions changed" + strings.Replace(of, " of ", ": ", 1)
		}
		old, cur := c.OldValue, c.NewValue
		if old == "" {
			old = "—"
		}
		if cur == "" {
			cur = "—"
		}
		return "Scope " + field + of + ": " + old + " → " + cur
	case store.ChangePolicyChanged, store.ChangeRewardsChanged:
		return describeRules(c)
	case store.ChangeAnnouncement:
		title, _, _ := strings.Cut(c.NewValue, "\n")
		return "Announcement: " + strings.TrimSpace(title)
	}
	return c.Kind
}

// websiteLink is a program's company website, ready to link to.
type websiteLink struct {
	URL   template.URL
	Label string // host without "www."
}

// httpURL is a platform-supplied URL if it's http(s), else "": for places
// html/template doesn't check as a URL, such as data-href, which app.js
// opens.
func httpURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.String()
}

// website returns a link for a program-supplied URL, or nil unless it is
// http(s). The scheme check makes template.URL safe here.
func website(raw string) *websiteLink {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil
	}
	return &websiteLink{URL: template.URL(u.String()), Label: strings.TrimPrefix(u.Hostname(), "www.")} //nolint:gosec // http(s) only, checked above
}

// programLink is a program in a list's first column (partials/pname.html),
// with what its link adds to the program page's address: a tab, say. Its
// methods give the variants some lists need.
type programLink struct {
	store.ProgramRef
	Suffix string
	// NoTag leaves out the platform's tag, on a page about one platform;
	// NoPrivate the "private" chip, in a list of invitations (all private).
	NoTag, NoPrivate bool
	// VDP and Ignored add those chips.
	VDP, Ignored bool
}

// plink is p for partials/pname.html, its link to the program page plus
// suffix ("?tab=stats#disclosed"), if given.
func plink(p store.ProgramRef, suffix ...string) programLink {
	return programLink{ProgramRef: p, Suffix: strings.Join(suffix, "")}
}

// WithoutTag is l without its platform's tag.
func (l programLink) WithoutTag() programLink { l.NoTag = true; return l }

// WithoutPrivate is l without the "private" chip.
func (l programLink) WithoutPrivate() programLink { l.NoPrivate = true; return l }

// Marked is l with a "VDP" chip for a program that pays no bounties and an
// "ignored" one for an ignored program.
func (l programLink) Marked(offersBounty, ignored bool) programLink {
	l.VDP, l.Ignored = !offersBounty, ignored
	return l
}
