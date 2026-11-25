package web

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"strings"

	"time"

	"github.com/mdpsec/huntdeck/internal/store"

	"github.com/mdpsec/huntdeck/internal/i18n"
	"github.com/mdpsec/huntdeck/internal/notify"
)

// The Notifications page shows what messages look like, and the channel
// form what a channel would have been sent in the last week; both from the
// changes recorded then (notify.PreviewChannel).

// previewWeek is how far back previews look.
const previewWeek = 7 * 24 * time.Hour

// cardAssets is how many assets an example message lists.
const cardAssets = 3

// messageCard is an example message (partials/notify.html's "message").
type messageCard struct {
	notify.Card
	Kind    string
	Tone    string // its bar's colour: good, accent, warn or bad
	At      time.Time
	Program *notify.Program
	Local   string // the program's page here
}

func messageCards(events []notify.Event, tr notify.Text) []messageCard {
	out := make([]messageCard, 0, len(events))
	for _, e := range events {
		c := messageCard{Card: e.CardIn(tr), Kind: e.Kind, Tone: eventTone(e.Kind), At: e.At, Program: e.Program}
		if len(c.Assets) > cardAssets {
			c.More += len(c.Assets) - cardAssets
			c.Assets = c.Assets[:cardAssets]
		}
		if p := e.Program; p != nil {
			c.Local = "/programs/" + url.PathEscape(p.Platform) + "/" + url.PathEscape(p.Handle)
		}
		out = append(out, c)
	}
	return out
}

func eventTone(kind string) string {
	switch kind {
	case notify.EventInvite, notify.EventLaunch, notify.EventReopened, notify.EventPlatformRecovered:
		return "good"
	case notify.EventScopeAdded, notify.EventBountyRaised, notify.EventAnnouncement, notify.EventReportBounty:
		return "accent"
	case notify.EventPlatformProblem:
		return "bad"
	}
	return "warn"
}

// previewView is what a channel would have been sent (partials/notify.html's
// "channel-preview").
type previewView struct {
	notify.Preview
	Cards []messageCard
	// Text is the message in the channel's own format, for kinds whose
	// shape the cards don't show (Bark, ServerChan).
	Text string
	// Parts is how many messages the text above would go out as (Bark and
	// ServerChan split long ones).
	Parts    int
	Sends    bool   // after each poll or with a digest; neither sends nothing
	Disable  bool   // the channel is switched off
	RateText string // how often messages would come, in the page's language
}

// rateText is how often messages would come, in words, in a language.
func rateText(lang string, m *i18n.Bundle, p notify.Preview) string {
	switch d := p.PerDay(); {
	case p.Messages == 0:
		return ""
	case d >= 1.5:
		return m.TF(lang, "about %.0f a day", d)
	case d >= .75:
		return m.T(lang, "about one a day")
	default:
		return m.TF(lang, "about %d a week", max(1, int(math.Round(d*7))))
	}
}

// preview works out what rules would have sent over the last week.
func (s *server) preview(ctx context.Context, lang, kind string, rules notify.Rules, instant bool, digestAt string, show int) (previewView, error) {
	now := s.Store.Now()
	since := now.Add(-previewWeek).Truncate(time.Hour) // an hour's steps, so the cached changes are reused
	changes, err := s.Store.NotifyChangesSince(ctx, since)
	if err != nil {
		return previewView{}, err
	}
	p := notify.PreviewChannel(changes, rules, instant, digestAt, s.location(ctx), since, now, show)
	tr := func(msg string, args ...any) string { return s.messages.TF(lang, msg, args...) }
	base := ""
	if set, err := s.Store.Settings(ctx, notify.BaseURLSetting); err == nil {
		base = strings.TrimRight(set[notify.BaseURLSetting], "/")
	}
	text, parts := notify.PreviewText(kind, p.Shown, now, base, s.location(ctx))
	return previewView{Preview: p, Cards: messageCards(p.Shown, tr), Text: text, Parts: parts, Sends: instant || digestAt != "", RateText: rateText(lang, s.messages, p)}, nil
}

// channelPreview is the channel form's preview for the choices in the
// query (the form's fields, without its address and secrets), as an HTML
// fragment the form swaps in as they change (static/channel.js).
func (s *server) channelPreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rules, _ := s.rulesFrom(q) // a half-typed field previews as if empty
	digestAt, err := notify.ParseDigestAt(strings.TrimSpace(q.Get("digest_at")))
	if err != nil {
		digestAt = ""
	}
	v, err := s.preview(r.Context(), s.langOf(r), oneOf(q.Get("kind"), store.NotifyDiscord, store.NotifyTelegram, store.NotifyWebhook, store.NotifyBark, store.NotifyServerChan), rules, isTrue(q.Get("instant")), digestAt, previewShown)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Disable = !isTrue(q.Get("enabled"))
	s.fragment(w, r, "notification", "channel-preview", v)
}

// previewShown is how many example messages the channel form shows.
const previewShown = 4
