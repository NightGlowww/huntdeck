package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/notify"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Notifier sends test messages (the notify.Notifier).
type Notifier interface {
	Test(ctx context.Context, c store.NotifyChannel) error
}

// channelKinds are the channel kinds, in display order.
var channelKinds = []struct{ Key, Label string }{
	{store.NotifyDiscord, "Discord"}, {store.NotifyTelegram, "Telegram"}, {store.NotifyWebhook, "Webhook"},
	{store.NotifyBark, "Bark"}, {store.NotifyServerChan, "ServerChan"},
}

func channelKindLabel(k string) string {
	for _, c := range channelKinds {
		if c.Key == k {
			return c.Label
		}
	}
	return k
}

type notificationsData struct {
	Channels   []channelRow
	Deliveries []store.Delivery
	// DeliveryFilter is what narrowed the history, and Channels names it
	// offers in the channel select.
	DeliveryChannel string
	DeliveryStatus  string
	BaseURL         string // saved address for links
	Suggested       string // how this browser reached HuntDeck
	Timezone        string
	Examples        previewView // this week's messages, every kind
}

type channelRow struct {
	store.NotifyChannel
	Where   string // address without secrets
	Events  string // what it sends, in words
	Filters string // and for which programs
	// Sent, Failed and Waiting count this channel's deliveries.
	Sent, Failed, Waiting int
}

// examplesShown is how many example messages the Notifications page shows.
const examplesShown = 3

// notifications lists the channels.
func (s *server) notifications(w http.ResponseWriter, r *http.Request) {
	s.renderNotifications(w, r, http.StatusOK, "")
}

func (s *server) renderNotifications(w http.ResponseWriter, r *http.Request, status int, flashErr string) {
	ctx := r.Context()
	chans, err := s.Store.NotifyChannels(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	d := notificationsData{Suggested: baseURL(r), Timezone: s.location(ctx).String()}
	stats, err := s.Store.DeliveryStats(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, c := range chans {
		st := stats[c.ID]
		rules := notify.ParseRules(c.Rules)
		lang := s.langOf(r)
		d.Channels = append(d.Channels, channelRow{NotifyChannel: c, Where: notify.ParseConfig(c.Config).Masked(c.Kind),
			Events: eventsTextLang(lang, s.messages, rules), Filters: filtersTextLang(lang, s.messages, rules),
			Sent: st.Sent, Failed: st.Failed, Waiting: st.Waiting})
	}
	d.DeliveryChannel = r.URL.Query().Get("channel")
	d.DeliveryStatus = r.URL.Query().Get("status")
	if d.Deliveries, err = s.Store.Deliveries(ctx, store.DeliveryFilter{Channel: d.DeliveryChannel, Status: d.DeliveryStatus, Limit: 50}); err != nil {
		s.serverError(w, r, err)
		return
	}
	var every notify.Rules
	for _, k := range notify.EventKinds {
		every.Events = append(every.Events, k.Key)
	}
	if d.Examples, err = s.preview(ctx, s.langOf(r), "", every, true, "", examplesShown); err != nil {
		s.serverError(w, r, err)
		return
	}
	set, err := s.Store.Settings(ctx, notify.BaseURLSetting)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	d.BaseURL = set[notify.BaseURLSetting]
	s.render(w, r, status, "notifications", pageData{Title: "Notifications", Nav: "notifications", Data: d, FlashErr: flashErr})
}

// eventsText is the kinds of event rules send, in words.
func eventsText(r notify.Rules) string {
	var kinds []string
	usual := true // the kinds a new channel sends
	for _, k := range notify.EventKinds {
		if r.Has(k.Key) {
			kinds = append(kinds, k.Short)
		}
		usual = usual && r.Has(k.Key) == k.Default
	}
	switch {
	case len(kinds) == len(notify.EventKinds):
		return "Everything"
	case len(kinds) == 0:
		return "Nothing"
	}
	t := strings.Join(kinds, ", ")
	if usual {
		return "The usual: " + t
	}
	return strings.ToUpper(t[:1]) + t[1:]
}

// filtersText is which programs rules send events about, in words.
func filtersText(r notify.Rules) string {
	var out []string
	if len(r.Platforms) > 0 {
		names := make([]string, len(r.Platforms))
		for i, p := range r.Platforms {
			names[i] = platformLabel(p)
		}
		out = append(out, strings.Join(names, ", "))
	}
	if r.BountyOnly {
		out = append(out, "paying only")
	}
	if r.PrivateOnly {
		out = append(out, "private only")
	}
	if r.MajorScopeOnly {
		out = append(out, "wildcards and new roots")
	}
	if r.MinBounty > 0 {
		out = append(out, "top bounty "+humanize.Money(r.MinBounty, "USD")+"+")
	}
	if len(r.Tags) > 0 {
		out = append(out, "tagged "+strings.Join(r.Tags, ", "))
	}
	if len(r.ExcludeTags) > 0 {
		out = append(out, "not tagged "+strings.Join(r.ExcludeTags, ", "))
	}
	if len(out) == 0 {
		return "all programs"
	}
	return strings.Join(out, " · ")
}

// deleteChannel removes a channel.
func (s *server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	c, ok := s.channelFromPath(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteNotifyChannel(r.Context(), c.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/notifications?msg=channel-deleted", http.StatusSeeOther)
}

// saveBaseURL stores HuntDeck's address for links in messages.
func (s *server) saveBaseURL(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	v := strings.TrimRight(strings.TrimSpace(r.PostFormValue("base_url")), "/")
	if v != "" {
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			s.renderNotifications(w, r, http.StatusBadRequest, "The address should look like https://huntdeck.your-tailnet.ts.net.")
			return
		}
	}
	err := s.Store.Write(r.Context(), func(tx *store.Tx) error {
		if v == "" {
			return tx.DeleteSetting(r.Context(), notify.BaseURLSetting)
		}
		return tx.PutSetting(r.Context(), notify.BaseURLSetting, v)
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/notifications?msg=address-saved", http.StatusSeeOther)
}
