package web

import (
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/mdpsec/huntdeck/internal/huntmail"
)

// The link a signup mail is about: verify the address, confirm the
// account, reset the password, sign in with a magic link, accept an
// invitation. HuntMail 1.2 and later pick it (its "action", the same
// rules, kept in step with its maintainer); for older ones HuntDeck picks
// it from the links and the text a reader sees beside each: the text
// first, then the address's host and path (never its query, where
// tracking tokens hold any letters), matched at the start of a word.

// mailAction is a message's action link and what it does.
type mailAction struct {
	URL  string // http(s) only
	Text string // as the sender wrote it
	Kind string // "Verify", "Reset", "Sign in" or "Invite"
}

type actionKind struct {
	kind      string
	text, url *regexp.Regexp
}

// actionKinds are tried in order, each on the links' text, then on their
// addresses.
var actionKinds = []actionKind{
	{"Verify", regexp.MustCompile(`verif|confirm|activat|validat|complete (your )?(registration|sign ?up)|finish (signing up|setting up)`),
		regexp.MustCompile(`(?:^|[^a-z0-9])(?:verif|confirm|activat|validat)`)},
	{"Reset", regexp.MustCompile(`reset|(set|choose|create|change) (a |your )?(new )?password|recover`),
		regexp.MustCompile(`(?:^|[^a-z0-9])(?:reset[-_/]?pass|pass(?:word)?[-_/]?(?:reset|change|forgot)|forgot[-_/]?pass|recover)`)},
	{"Sign in", regexp.MustCompile(`magic link|(sign|log) ?in (with this link|now|securely)|one[- ]click (sign|log) ?in`),
		regexp.MustCompile(`(?:^|[^a-z0-9])(?:magic|login[-_]?token|signin[-_]?token)`)},
	{"Invite", regexp.MustCompile(`accept (the |your |this )?invit|join (the |your )?(team|workspace|organi[sz]ation|project)`),
		regexp.MustCompile(`(?:^|[^a-z0-9])invit(?:e|ation)s?(?:[^a-z0-9]|$)`)},
}

// notActions are links a mail always carries that are never what it's
// about.
var notActions = regexp.MustCompile(`unsubscribe|opt[- ]?out|preferences|privacy|terms|policy|view (it |this )?(email |message )?in (a |your )?browser|webversion|` +
	`facebook|twitter|x\.com/|linkedin|instagram|youtube|tiktok|apps\.apple|play\.google|/help|support|contact us|manage (your )?(account|subscription|email)`)

// Action is the message's action link, nil without one: HuntMail's when
// it says (an http(s) one of a kind HuntDeck knows), else HuntDeck's pick.
func (m mailRow) Action() *mailAction {
	if a := m.Email.Action; a != nil {
		if u := httpURL(a.URL); u != "" && slices.ContainsFunc(actionKinds, func(k actionKind) bool { return k.kind == a.Kind }) {
			return &mailAction{URL: u, Text: strings.TrimSpace(a.Text), Kind: a.Kind}
		}
		return nil
	}
	for _, k := range actionKinds {
		for _, byURL := range []bool{false, true} {
			for _, l := range m.LinkDetails {
				u := httpURL(l.URL)
				if u == "" {
					continue
				}
				text, addr := strings.ToLower(strings.TrimSpace(l.Text)), strings.ToLower(u)
				if strings.Contains(text, "://") || strings.HasPrefix(text, "www.") {
					text = "" // an address as its text: the address rules decide
				}
				if notActions.MatchString(text) || notActions.MatchString(addr) {
					continue
				}
				if !byURL && text != "" && k.text.MatchString(text) || byURL && k.url.MatchString(hostPath(u)) {
					return &mailAction{URL: u, Text: strings.TrimSpace(l.Text), Kind: k.kind}
				}
			}
		}
	}
	return nil
}

// hostPath is an address's host and path, lowercased: what the URL rules
// look at.
func hostPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host + u.EscapedPath())
}

// mailLinks are a message's links as its Links box shows them: what the
// mail is about (its content and action links) first, the sender's own
// (social, legal, unsubscribe, …, as HuntMail tells them) folded away.
type mailLinks struct {
	Main, Other []huntmail.Link
	OtherKinds  string // "social, legal and 2 more", for the fold
}

// Links sorts the message's links for its Links box.
func (m *mailMessageView) Links() mailLinks {
	var out mailLinks
	var kinds []string
	for _, l := range m.LinkDetails {
		if l.Kind == huntmail.LinkContent || l.Kind == huntmail.LinkAction {
			out.Main = append(out.Main, l)
			continue
		}
		out.Other = append(out.Other, l)
		if !slices.Contains(kinds, l.Kind) {
			kinds = append(kinds, l.Kind)
		}
	}
	if len(kinds) > 3 {
		kinds = append(kinds[:3], strconv.Itoa(len(kinds)-3)+" more")
	}
	out.OtherKinds = strings.Join(kinds, ", ")
	return out
}

// LinksOpen says the Links box starts open: a few links, and no action
// link above it.
func (m *mailMessageView) LinksOpen() bool {
	return len(m.Links().Main) <= 3 && m.Action() == nil
}
