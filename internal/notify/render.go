package notify

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/reports"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Message size limits, below each service's own (Discord 2,000 characters,
// Telegram 4,096).
const (
	discordLimit  = 1900
	telegramLimit = 4000
	assetsShown   = 10 // assets listed per event; the rest are counted
	digestEvents  = 40 // events listed in one digest
)

// style is a chat service's text markup.
type style interface {
	esc(s string) string           // plain text
	bold(s string) string          // s already escaped
	code(s string) string          // raw s, shown as code
	link(text, href string) string // text already escaped
}

type discordStyle struct{}

// discordEscaper neutralises Markdown and mentions in text from platforms.
// Parentheses only matter after a ']', and lines never start with such
// text, so headings can't start either; '>' is escaped anyway.
var discordEscaper = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "~", `\~`, "`", "\\`", "|", `\|`,
	">", `\>`, "[", `\[`, "]", `\]`, "@", "@\u200b")

func (discordStyle) esc(s string) string  { return discordEscaper.Replace(s) }
func (discordStyle) bold(s string) string { return "**" + s + "**" }
func (discordStyle) code(s string) string { return "`" + strings.ReplaceAll(s, "`", "ˋ") + "`" }
func (discordStyle) link(text, href string) string {
	return "[" + text + "](" + strings.NewReplacer("(", "%28", ")", "%29", " ", "%20").Replace(href) + ")"
}

type telegramStyle struct{}

func (telegramStyle) esc(s string) string  { return html.EscapeString(s) }
func (telegramStyle) bold(s string) string { return "<b>" + s + "</b>" }
func (telegramStyle) code(s string) string { return "<code>" + html.EscapeString(s) + "</code>" }
func (telegramStyle) link(text, href string) string {
	return `<a href="` + html.EscapeString(href) + `">` + text + "</a>"
}

// renderer writes events as chat messages.
type renderer struct {
	st   style
	base string // HuntDeck's address for links, or ""
	loc  *time.Location
}

var emoji = map[string]string{
	EventInvite: "🔒", EventLaunch: "🚀", EventScopeAdded: "🎯", EventScopeRemoved: "➖", EventScopeOut: "🚫",
	EventScopeChanged: "✏️", EventReopened: "▶️", EventPaused: "⏸️", EventRemoved: "🗑️", EventBountyRaised: "💰",
	EventRewardsChanged: "💱", EventPolicyChanged: "📝", EventAnnouncement: "📣", EventScanHandoff: "🛰️", EventScanBlocked: "🛑", EventPlatformProblem: "⚠️", EventPlatformRecovered: "✅", EventTest: "🔔",
	EventReportBounty: "💵", EventReportState: "📄", EventReportWaiting: "💬", EventReportSeverity: "🎚️", EventReportNew: "📨", EventReportQuiet: "📭",
}

// parts renders a message's events as one or more messages under limit.
func (r renderer) parts(kind string, events []Event, at time.Time, limit int) []string {
	var blocks []string
	if kind == store.OutboxDigest {
		h := r.st.bold(r.st.esc("HuntDeck daily digest")) + " · " + r.st.esc(at.In(r.loc).Format("Mon 2 Jan"))
		if len(events) > 1 {
			h += " · " + strconv.Itoa(len(events)) + " updates"
		}
		blocks = append(blocks, h)
	}
	for i, e := range events {
		if kind == store.OutboxDigest && i == digestEvents {
			more := r.st.esc(fmt.Sprintf("…and %d more updates", len(events)-i))
			if r.base != "" {
				more = r.st.link(more, r.base+"/changes?since=24h")
			}
			blocks = append(blocks, more)
			break
		}
		blocks = append(blocks, r.event(e))
	}
	return chunk(blocks, limit)
}

// chunk joins blocks into messages of at most limit characters, splitting
// between blocks, or between lines of a block that's too long by itself.
func chunk(blocks []string, limit int) []string {
	var out []string
	cur := ""
	flush := func() {
		if cur != "" {
			out = append(out, cur)
			cur = ""
		}
	}
	for _, b := range blocks {
		if len(b) > limit {
			flush()
			for _, line := range strings.Split(b, "\n") {
				if len(line) > limit {
					line = truncate(line, limit)
				}
				if cur != "" && len(cur)+1+len(line) > limit {
					flush()
				}
				cur = joinNonEmpty(cur, line, "\n")
			}
			flush()
			continue
		}
		if cur != "" && len(cur)+2+len(b) > limit {
			flush()
		}
		cur = joinNonEmpty(cur, b, "\n\n")
	}
	flush()
	return out
}

func joinNonEmpty(a, b, sep string) string {
	if a == "" {
		return b
	}
	return a + sep + b
}

// truncate cuts s to at most n bytes on a rune boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

func (r renderer) programURL(p *Program) string {
	if r.base != "" {
		return r.base + "/programs/" + url.PathEscape(p.Platform) + "/" + url.PathEscape(p.Handle)
	}
	return p.URL
}

// event renders one event: a title line, the program, then details.
func (r renderer) event(e Event) string {
	st, c := r.st, e.Card()
	title := c.Emoji + " " + st.bold(st.esc(c.Title))
	if e.Kind == EventTest {
		return title
	}
	lines := []string{title + " · " + st.esc(c.Platform)}
	if e.Report != nil {
		lines = append(lines, r.reportLine(e.Report))
		if e.Detail != "" {
			lines = append(lines, st.esc(e.Detail))
		}
		return strings.Join(lines, "\n")
	}
	if p := e.Program; p != nil {
		about := []string{st.link(st.bold(st.esc(p.Name)), r.programURL(p))}
		for _, a := range c.About {
			about = append(about, st.esc(a))
		}
		lines = append(lines, strings.Join(about, " · "))
	}
	if c.Detail != "" {
		d := st.esc(c.Detail)
		if e.ChangeID != 0 && r.base != "" {
			d += " · " + st.link(st.esc("view diff"), r.base+"/changes/"+strconv.FormatInt(e.ChangeID, 10))
		}
		lines = append(lines, d)
	}
	for _, a := range c.Assets {
		line := "• " + st.code(a.Identifier)
		if a.Notes != "" {
			line += st.esc(" (" + a.Notes + ")")
		}
		lines = append(lines, line)
	}
	if c.More > 0 {
		more := st.esc(fmt.Sprintf("…and %d more", c.More))
		if r.base != "" && e.Program != nil {
			more = st.link(more, r.programURL(e.Program))
		}
		lines = append(lines, more)
	}
	return strings.Join(lines, "\n")
}

// Text renders a card's words in a language, with arguments; nil keeps them
// English (deliveries to Discord, Telegram and webhooks are English).
type Text func(msg string, args ...any) string

func text(tr Text, msg string, args ...any) string {
	if tr == nil {
		return fmt.Sprintf(msg, args...)
	}
	return tr(msg, args...)
}

// Card is an event's parts as plain text, the same in every chat message
// and on the web page's examples.
type Card struct {
	Emoji    string
	Title    string   // "3 new in-scope assets"
	Platform string   // its name
	About    []string // the program: "BBP, up to $3,000", "private", "HuntScore 52"
	Detail   string
	Assets   []CardAsset // the first few
	More     int         // assets not listed
}

// CardAsset is an asset in a Card.
type CardAsset struct {
	Identifier string
	Notes      string // "wildcard, no bounty"
}

// Card returns e's parts, in English.
func (e Event) Card() Card { return e.CardIn(nil) }

// CardIn returns e's parts with their words from tr (the web page's
// language); tr nil is English.
func (e Event) CardIn(tr Text) Card {
	c := Card{Emoji: emoji[e.Kind], Title: title(e, tr), Platform: platformLabel(e.Platform), Detail: e.Detail}
	if p := e.Program; p != nil {
		c.Platform = platformLabel(p.Platform)
		if p.Bounty {
			b := "BBP"
			if p.TopBounty != nil && *p.TopBounty > 0 {
				b = text(tr, "BBP, up to %s", humanize.MoneyFull(*p.TopBounty, p.Currency))
			}
			c.About = append(c.About, b)
		} else {
			c.About = append(c.About, text(tr, "VDP"))
		}
		if p.Private {
			c.About = append(c.About, text(tr, "private"))
		}
		if p.HuntScore != nil {
			c.About = append(c.About, text(tr, "HuntScore %.0f", *p.HuntScore))
		}
	}
	if rep := e.Report; rep != nil {
		c.About = nil
		c.Detail = rep.ProgramName + " · " + rep.Title
		if e.Detail != "" {
			c.Detail += " · " + e.Detail
		}
	}
	for i, a := range e.Assets {
		if i == assetsShown {
			break
		}
		c.Assets = append(c.Assets, CardAsset{a.Identifier, strings.Join(assetNotes(e.Kind, a, tr), ", ")})
	}
	c.More = e.count() - len(c.Assets)
	return c
}

// title is an event's headline.
func title(e Event, tr Text) string {
	n := e.count()
	assets := func() string {
		if n == 1 {
			return text(tr, "%d asset", n)
		}
		return text(tr, "%d assets", n)
	}
	switch e.Kind {
	case EventInvite:
		return text(tr, "New private invite")
	case EventLaunch:
		return text(tr, "New program")
	case EventScopeAdded:
		if n == 1 {
			return text(tr, "%d new in-scope asset", n)
		}
		return text(tr, "%d new in-scope assets", n)
	case EventScopeRemoved:
		return text(tr, "%s taken out of scope", assets())
	case EventScopeOut:
		return text(tr, "Out-of-scope list changed (%s)", assets())
	case EventScopeChanged:
		if n == 1 {
			return text(tr, "%d scope entry edited", n)
		}
		return text(tr, "%d scope entries edited", n)
	case EventReopened:
		return text(tr, "Reopened")
	case EventPaused:
		if e.New == "closed" {
			return text(tr, "Closed")
		}
		return text(tr, "Paused")
	case EventRemoved:
		return text(tr, "No longer listed")
	case EventBountyRaised:
		return text(tr, "Bounties raised")
	case EventRewardsChanged:
		return text(tr, "Bounty table changed")
	case EventPolicyChanged:
		return text(tr, "Rules edited")
	case EventScanHandoff:
		if e.Detail != "" {
			return text(tr, "Scan handoff: %s", e.Detail)
		}
		return text(tr, "Targets handed to the scanner")
	case EventScanBlocked:
		return text(tr, "Scan batch blocked")
	case EventAnnouncement:
		return text(tr, "Announcement")
	case EventPlatformProblem:
		return text(tr, "Polls failing")
	case EventPlatformRecovered:
		return text(tr, "Polls working again")
	case EventTest:
		return text(tr, "Test message: this channel works")
	case EventReportBounty:
		return text(tr, "Bounty awarded")
	case EventReportState:
		return text(tr, "Report %s", strings.ToLower(reports.Label(e.New)))
	case EventReportWaiting:
		return text(tr, "Waiting on you")
	case EventReportSeverity:
		return text(tr, "Severity changed")
	case EventReportNew:
		return text(tr, "New report found")
	case EventReportQuiet:
		return text(tr, "Report gone quiet")
	}
	return e.Kind
}

// assetNotes are what to say about an asset in an event of kind.
func assetNotes(kind string, a Asset, tr Text) []string {
	var n []string
	if a.Type == scope.AssetWildcard {
		n = append(n, text(tr, "wildcard"))
	}
	if a.NewRoot {
		n = append(n, text(tr, "new root domain"))
	}
	if kind == EventScopeOut {
		if a.InScope {
			n = append(n, text(tr, "now in scope"))
		} else {
			n = append(n, text(tr, "out of scope"))
		}
	}
	if kind == EventScopeAdded && !a.Bounty {
		n = append(n, text(tr, "no bounty"))
	} else if kind == EventScopeAdded && a.MaxSeverity != "" && !strings.EqualFold(a.MaxSeverity, "none") {
		n = append(n, text(tr, "up to %s", strings.ToLower(a.MaxSeverity)))
	}
	return n
}

// WebhookPayloadVersion is the webhook JSON format's version.
const WebhookPayloadVersion = 1

// webhookBody is the JSON a webhook gets: the events, with links to
// HuntDeck when its address is known.
func webhookBody(kind string, events []Event, at time.Time, base string) ([]byte, error) {
	out := make([]Event, len(events))
	for i, e := range events {
		if e.Program != nil && base != "" {
			p := *e.Program
			p.HuntDeckURL = renderer{base: base}.programURL(&p)
			e.Program = &p
		}
		if e.Report != nil && base != "" {
			rep := *e.Report
			rep.HuntDeckURL = renderer{base: base}.reportURL(&rep)
			e.Report = &rep
		}
		out[i] = e
	}
	return json.Marshal(struct {
		Version int       `json:"version"`
		Type    string    `json:"type"` // poll, digest or test
		SentAt  time.Time `json:"sent_at"`
		Events  []Event   `json:"events"`
	}{WebhookPayloadVersion, kind, at.UTC(), out})
}

// PreviewText is what one message of these events would look like in a
// channel kind's own format: the settings form shows it for the kinds whose
// shape matters (Bark's short notification, ServerChan's Markdown article),
// where the generic cards would not tell the whole story.
func PreviewText(kind string, events []Event, at time.Time, base string, loc *time.Location) (string, int) {
	var st style
	var limit int
	switch kind {
	case store.NotifyBark:
		st, limit = plainStyle{}, barkLimit
	case store.NotifyServerChan:
		st, limit = discordStyle{}, serverChanLimit
	default:
		return "", 0
	}
	if len(events) == 0 {
		return "", 0
	}
	parts := renderer{st: st, base: base, loc: loc}.parts(store.OutboxPoll, events, at, limit)
	return strings.Join(parts, "\n\n"), len(parts)
}

// plainStyle is a channel that shows plain text (Bark): no markup, and a
// link keeps its address in brackets so it stays usable.
type plainStyle struct{}

func (plainStyle) esc(s string) string  { return s }
func (plainStyle) bold(s string) string { return s }
func (plainStyle) code(s string) string { return s }
func (plainStyle) link(text, href string) string {
	if href == "" || strings.Contains(text, href) {
		return text
	}
	return text + " (" + href + ")"
}
