package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mdpsec/huntdeck/internal/notify"
	"github.com/mdpsec/huntdeck/internal/store"
)

// channelForm is the add/edit channel form.
type channelForm struct {
	ID     int64
	Kind   string
	Name   string
	ChatID string // Telegram's isn't secret, so it's shown
	// Bark's group, sound and level: none of them is secret, so the form
	// shows them back and saving an edit keeps them.
	Group     string
	Sound     string
	Level     string
	HasSecret bool   // a webhook secret is set
	Where     string // the saved address, masked
	Rules     notify.Rules
	Enabled   bool
	Instant   bool
	DigestAt  string
	Error     string
	Flash     string
	Groups    []eventGroup
	Platforms []string
	Timezone  string
	Preview   previewView
}

// eventGroup is a group of event kinds in the form.
type eventGroup struct {
	Title string
	Kinds []notify.EventKind
}

// Title is the form's heading: the channel's name, or what's being added.
func (f channelForm) Title() string {
	if f.ID != 0 {
		return f.Name
	}
	return "Add " + channelKindLabel(f.Kind) + " channel"
}

// HasEvent and HasPlatform report whether the form has an event or
// platform ticked.
func (f channelForm) HasEvent(k string) bool { return f.Rules.Has(k) }
func (f channelForm) HasPlatform(p string) bool {
	for _, v := range f.Rules.Platforms {
		if v == p {
			return true
		}
	}
	return false
}

// TagsText and ExcludeText show the tag filters as typed.
func (f channelForm) TagsText() string    { return strings.Join(f.Rules.Tags, ", ") }
func (f channelForm) ExcludeText() string { return strings.Join(f.Rules.ExcludeTags, ", ") }

func (s *server) newChannelForm(r *http.Request, kind string) channelForm {
	f := channelForm{Kind: kind, Rules: notify.DefaultRules(), Enabled: true, Instant: true,
		Platforms: s.platformNames(), Timezone: s.location(r.Context()).String()}
	for _, k := range notify.EventKinds {
		if n := len(f.Groups); n == 0 || f.Groups[n-1].Title != k.Group {
			f.Groups = append(f.Groups, eventGroup{Title: k.Group})
		}
		g := &f.Groups[len(f.Groups)-1]
		g.Kinds = append(g.Kinds, k)
	}
	return f
}

// newChannel shows an empty form for a kind of channel.
func (s *server) newChannel(w http.ResponseWriter, r *http.Request) {
	kind := oneOf(r.URL.Query().Get("kind"), store.NotifyDiscord, store.NotifyTelegram, store.NotifyWebhook, store.NotifyBark, store.NotifyServerChan)
	s.renderChannel(w, r, http.StatusOK, s.newChannelForm(r, kind))
}

// editChannel shows a channel's form.
func (s *server) editChannel(w http.ResponseWriter, r *http.Request) {
	c, ok := s.channelFromPath(w, r)
	if !ok {
		return
	}
	cfg := notify.ParseConfig(c.Config)
	f := s.newChannelForm(r, c.Kind)
	f.ID, f.Name, f.ChatID, f.HasSecret, f.Where = c.ID, c.Name, cfg.ChatID, cfg.Secret != "", cfg.Masked(c.Kind)
	f.Group, f.Sound, f.Level = cfg.Group, cfg.Sound, cfg.Level
	f.Rules, f.Enabled, f.Instant, f.DigestAt = notify.ParseRules(c.Rules), c.Enabled, c.Instant, c.DigestAt
	s.renderChannel(w, r, http.StatusOK, f)
}

func (s *server) renderChannel(w http.ResponseWriter, r *http.Request, status int, f channelForm) {
	var err error
	if f.Preview, err = s.preview(r.Context(), s.langOf(r), f.Kind, f.Rules, f.Instant, f.DigestAt, previewShown); err != nil {
		s.serverError(w, r, err)
		return
	}
	f.Preview.Disable = !f.Enabled
	s.render(w, r, status, "notification", pageData{Title: f.Title() + " · Notifications", Nav: "notifications", Data: f,
		Flash: f.Flash, FlashErr: f.Error})
}

func (s *server) channelFromPath(w http.ResponseWriter, r *http.Request) (*store.NotifyChannel, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.renderError(w, r, http.StatusNotFound, "No such channel.")
		return nil, false
	}
	c, err := s.Store.NotifyChannel(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "No such channel.")
		return nil, false
	}
	if err != nil {
		s.serverError(w, r, err)
		return nil, false
	}
	return c, true
}

// rulesFrom reads a channel's rules from the form's fields, with what's
// wrong with them, if anything.
func (s *server) rulesFrom(form url.Values) (notify.Rules, string) {
	r := notify.Rules{BountyOnly: isTrue(form.Get("bounty_only")), PrivateOnly: isTrue(form.Get("private_only")),
		MajorScopeOnly: isTrue(form.Get("major_scope_only"))}
	for _, k := range notify.EventKinds {
		if isTrue(form.Get("event_" + k.Key)) {
			r.Events = append(r.Events, k.Key)
		}
	}
	for _, p := range s.platformNames() {
		if isTrue(form.Get("platform_" + p)) {
			r.Platforms = append(r.Platforms, p)
		}
	}
	var err error
	problem := ""
	if r.Tags, err = store.NormalizeTags(form.Get("tags")); err != nil {
		problem = "Only programs tagged: " + err.Error() + "."
	}
	if r.ExcludeTags, err = store.NormalizeTags(form.Get("exclude_tags")); err != nil && problem == "" {
		problem = "Never programs tagged: " + err.Error() + "."
	}
	if v := strings.TrimSpace(form.Get("min_bounty")); v != "" {
		if r.MinBounty, err = strconv.ParseFloat(v, 64); err != nil || r.MinBounty < 0 || r.MinBounty > 1e7 {
			r.MinBounty = 0
			if problem == "" {
				problem = "Minimum top bounty should be a number like 1000."
			}
		}
	}
	if len(r.Events) == 0 && problem == "" {
		problem = "Pick at least one event."
	}
	return r, problem
}

// channelDraft is a channel as its form has it: the saved channel, if
// any, with the form's changes.
type channelDraft struct {
	c       store.NotifyChannel
	cfg     notify.Config
	f       channelForm
	invalid string // why it can't be saved, if it can't
}

// readDraft reads the channel form; a channel id in the path is the
// channel being edited. It writes the response itself when it returns
// false.
func (s *server) readDraft(w http.ResponseWriter, r *http.Request) (channelDraft, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return channelDraft{}, false
	}
	var d channelDraft
	if r.PathValue("id") != "" {
		old, ok := s.channelFromPath(w, r)
		if !ok {
			return d, false
		}
		d.c, d.cfg = *old, notify.ParseConfig(old.Config)
	} else {
		d.c.Kind = oneOf(r.PostFormValue("kind"), store.NotifyDiscord, store.NotifyTelegram, store.NotifyWebhook, store.NotifyBark, store.NotifyServerChan)
	}
	f := s.newChannelForm(r, d.c.Kind)
	f.ID, f.Where, f.HasSecret = d.c.ID, d.cfg.Masked(d.c.Kind), d.cfg.Secret != ""

	form := r.PostForm
	f.Name = strings.TrimSpace(form.Get("name"))
	f.Enabled, f.Instant = isTrue(form.Get("enabled")), isTrue(form.Get("instant"))
	f.DigestAt = strings.TrimSpace(form.Get("digest_at"))
	f.ChatID = strings.TrimSpace(form.Get("chat_id"))
	f.Rules, d.invalid = s.rulesFrom(form)
	if f.Name == "" || len(f.Name) > 60 {
		d.invalid = "Give the channel a name of up to 60 characters."
	}
	digestAt, err := notify.ParseDigestAt(f.DigestAt)
	switch {
	case d.invalid != "":
	case err != nil:
		d.invalid = "Daily digest: " + err.Error() + "."
	case !f.Instant && digestAt == "":
		d.invalid = "Send each poll's events, a daily digest, or both."
	}
	if err == nil {
		f.DigestAt = digestAt
	}

	// Secrets left blank keep the saved ones.
	if v := strings.TrimSpace(form.Get("url")); v != "" {
		d.cfg.URL = v
	}
	if v := strings.TrimSpace(form.Get("token")); v != "" {
		d.cfg.Token = v
	}
	if v := strings.TrimSpace(form.Get("secret")); v != "" {
		d.cfg.Secret = v
	}
	if v := strings.TrimSpace(form.Get("server")); v != "" {
		d.cfg.Server = v
	}
	if v := strings.TrimSpace(form.Get("device_key")); v != "" {
		d.cfg.DeviceKey = v
	}
	if v := strings.TrimSpace(form.Get("send_key")); v != "" {
		d.cfg.SendKey = v
	}
	if form.Has("group") {
		d.cfg.Group = strings.TrimSpace(form.Get("group"))
	}
	if form.Has("sound") {
		d.cfg.Sound = strings.TrimSpace(form.Get("sound"))
	}
	if form.Has("level") {
		d.cfg.Level = strings.TrimSpace(form.Get("level"))
	}
	if isTrue(form.Get("clear_secret")) {
		d.cfg.Secret = ""
	}
	if form.Has("chat_id") {
		d.cfg.ChatID = f.ChatID
	}
	if err := d.cfg.Validate(d.c.Kind); err != nil && d.invalid == "" {
		d.invalid = sentence(err)
	}
	d.f = f
	return d, true
}

// sentence is an error as a sentence for the UI.
func sentence(err error) string {
	m := err.Error()
	return strings.ToUpper(m[:1]) + m[1:] + "."
}

// saveChannel creates a channel (no id in the path) or updates one.
func (s *server) saveChannel(w http.ResponseWriter, r *http.Request) {
	d, ok := s.readDraft(w, r)
	if !ok {
		return
	}
	if d.invalid != "" {
		d.f.Error = d.invalid
		s.renderChannel(w, r, http.StatusBadRequest, d.f)
		return
	}
	c, f := d.c, d.f
	if f.DigestAt != "" && f.DigestAt != c.DigestAt {
		// Start with the next digest time, not one already past today.
		slot := notify.LastSlot(f.DigestAt, s.Store.Now(), s.location(r.Context()))
		c.DigestSentAt = &slot
	} else {
		c.DigestSentAt = nil // keeps the saved one
	}
	c.Name, c.Config, c.Rules = f.Name, d.cfg.JSON(), f.Rules.JSON()
	c.Enabled, c.Instant, c.DigestAt = f.Enabled, f.Instant, f.DigestAt
	if _, err := s.Store.SaveNotifyChannel(r.Context(), c); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/notifications?msg=channel-saved", http.StatusSeeOther)
}

// testChannel sends a test message now and says how it went. From the
// Notifications page it tests a saved channel; from the channel form, the
// address as the form has it (saved or not), and with ?inline=1, the
// form's script gets the outcome as a line of text.
func (s *server) testChannel(w http.ResponseWriter, r *http.Request) {
	d, ok := s.readDraft(w, r)
	if !ok {
		return
	}
	inline, fromForm := r.URL.Query().Get("inline") == "1", r.PostForm.Has("name")
	name := d.f.Name
	if !fromForm {
		name = d.c.Name
	}
	if name == "" {
		name = "the channel"
	}
	status, msg := http.StatusOK, "Test message sent to "+name+"."
	switch err := d.cfg.Validate(d.c.Kind); {
	case s.Notify == nil:
		status, msg = http.StatusServiceUnavailable, "Notifications can't be sent from this process."
	case err != nil:
		status, msg = http.StatusBadRequest, sentence(err)
	default:
		c := d.c
		c.Config = d.cfg.JSON()
		if err := s.Notify.Test(r.Context(), c); err != nil {
			status, msg = http.StatusBadGateway, "The test message to "+name+" failed: "+err.Error()
		}
	}
	switch {
	case inline:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(msg))
	case fromForm:
		if status == http.StatusOK {
			d.f.Flash = msg
		} else {
			d.f.Error = msg
		}
		s.renderChannel(w, r, status, d.f)
	case status != http.StatusOK:
		s.renderNotifications(w, r, status, msg)
	default:
		http.Redirect(w, r, "/settings/notifications?msg=test-sent", http.StatusSeeOther)
	}
}
