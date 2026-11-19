package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Rules choose what a channel is sent (stored as JSON).
type Rules struct {
	Events      []string `json:"events"`
	Platforms   []string `json:"platforms,omitempty"` // empty: all
	BountyOnly  bool     `json:"bounty_only,omitempty"`
	PrivateOnly bool     `json:"private_only,omitempty"`
	// MinBounty keeps programs whose top bounty is at least this much;
	// programs whose top bounty isn't known pass.
	MinBounty   float64  `json:"min_bounty,omitempty"`
	Tags        []string `json:"tags,omitempty"`         // programs with any of these (empty: all)
	ExcludeTags []string `json:"exclude_tags,omitempty"` // never programs with these
	// MajorScopeOnly keeps only wildcards and new root domains in "scope
	// added" events.
	MajorScopeOnly bool `json:"major_scope_only,omitempty"`
}

// DefaultRules are a new channel's rules.
func DefaultRules() Rules {
	var r Rules
	for _, k := range EventKinds {
		if k.Default {
			r.Events = append(r.Events, k.Key)
		}
	}
	return r
}

// ParseRules reads stored rules; unreadable ones send nothing.
func ParseRules(s string) Rules {
	var r Rules
	_ = json.Unmarshal([]byte(s), &r)
	return r
}

// JSON encodes the rules for storage.
func (r Rules) JSON() string {
	b, _ := json.Marshal(r) // plain fields; can't fail
	return string(b)
}

// Has reports whether the rules send events of kind.
func (r Rules) Has(kind string) bool {
	return slices.Contains(r.Events, kind) || kind == EventPlatformRecovered && slices.Contains(r.Events, EventPlatformProblem)
}

// Match returns e as this channel should get it, or false to skip it.
func (r Rules) Match(e Event) (Event, bool) {
	if !r.Has(e.Kind) {
		return e, false
	}
	if len(r.Platforms) > 0 && !slices.Contains(r.Platforms, e.Platform) {
		return e, false
	}
	p := e.Program
	if p == nil { // platform events
		return e, true
	}
	switch {
	case p.ignored,
		r.BountyOnly && !p.Bounty,
		r.PrivateOnly && !p.Private,
		r.MinBounty > 0 && p.TopBounty != nil && *p.TopBounty < r.MinBounty,
		len(r.Tags) > 0 && !slices.ContainsFunc(p.Tags, func(t string) bool { return slices.Contains(r.Tags, t) }),
		slices.ContainsFunc(p.Tags, func(t string) bool { return slices.Contains(r.ExcludeTags, t) }):
		return e, false
	}
	if r.MajorScopeOnly && e.Kind == EventScopeAdded {
		var keep []Asset
		for _, a := range e.Assets {
			if a.Type == scope.AssetWildcard || a.NewRoot {
				keep = append(keep, a)
			}
		}
		if len(keep) == 0 {
			return e, false
		}
		e.Assets, e.AssetsTotal = keep, 0
	}
	return e, true
}

// Config is a channel's address and secrets (stored as JSON).
type Config struct {
	URL    string `json:"url,omitempty"`     // Discord webhook or webhook URL
	Token  string `json:"token,omitempty"`   // Telegram bot token
	ChatID string `json:"chat_id,omitempty"` // Telegram chat
	Secret string `json:"secret,omitempty"`  // webhook signing secret, optional
	// Server is a Bark or ServerChan base address ("https://api.day.app",
	// "https://sctapi.ftqq.com"); "" uses theirs.
	Server    string `json:"server,omitempty"`
	DeviceKey string `json:"device_key,omitempty"` // Bark device key
	Group     string `json:"group,omitempty"`      // Bark notification group (default HuntDeck)
	Sound     string `json:"sound,omitempty"`      // Bark sound name (its default when empty)
	Level     string `json:"level,omitempty"`      // Bark interruption level: active, timeSensitive or passive
	SendKey   string `json:"send_key,omitempty"`   // ServerChan SendKey
}

// ParseConfig reads a stored config.
func ParseConfig(s string) Config {
	var c Config
	_ = json.Unmarshal([]byte(s), &c)
	return c
}

// JSON encodes the config for storage.
func (c Config) JSON() string {
	b, _ := json.Marshal(c) //nolint:gosec // stored in the database on purpose; never logged or shown (see Masked)
	return string(b)
}

var (
	discordWebhook = regexp.MustCompile(`^https://(?:(?:ptb|canary)\.)?discord(?:app)?\.com/api/(?:v[0-9]+/)?webhooks/[0-9]+/[A-Za-z0-9_-]+$`)
	telegramToken  = regexp.MustCompile(`^[0-9]{3,}:[A-Za-z0-9_-]{20,}$`)
	telegramChat   = regexp.MustCompile(`^(?:-?[0-9]+|@[A-Za-z0-9_]{4,})$`)
)

// Validate checks a config for a channel kind; errors are safe to show.
func (c Config) Validate(kind string) error {
	switch kind {
	case store.NotifyDiscord:
		if !discordWebhook.MatchString(c.URL) {
			return errors.New("the Discord webhook URL should look like https://discord.com/api/webhooks/123/abc (channel settings › Integrations › Webhooks › Copy Webhook URL)")
		}
	case store.NotifyTelegram:
		if !telegramToken.MatchString(c.Token) {
			return errors.New("the Telegram bot token should look like 123456:ABC-… (from @BotFather)")
		}
		if !telegramChat.MatchString(c.ChatID) {
			return errors.New("the Telegram chat ID should be a number (negative for groups) or @channelname")
		}
	case store.NotifyBark:
		if err := checkServer(c.Server); err != nil {
			return err
		}
		if !barkDeviceKey.MatchString(c.DeviceKey) {
			return errors.New("the Bark device key is the last part of the push address (https://api.day.app/<key>/…)")
		}
		if len([]rune(c.Group)) > 40 {
			return errors.New("the Bark group is at most 40 characters")
		}
		if len([]rune(c.Sound)) > 40 {
			return errors.New("the Bark sound name is at most 40 characters")
		}
		if c.Level != "" && c.Level != "active" && c.Level != "timeSensitive" && c.Level != "passive" {
			return errors.New("the Bark level is active, timeSensitive or passive")
		}
	case store.NotifyServerChan:
		if err := checkServer(c.Server); err != nil {
			return err
		}
		if !serverChanKey.MatchString(c.SendKey) {
			return errors.New("the ServerChan SendKey looks like SCT123456T… (from sct.ftqq.com)")
		}
	case store.NotifyWebhook:
		u, err := url.Parse(c.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return errors.New("the webhook URL should be an http or https URL")
		}
	default:
		return fmt.Errorf("unknown channel kind %q", kind)
	}
	return nil
}

// Masked shows where a channel sends without its secrets.
func (c Config) Masked(kind string) string {
	switch kind {
	case store.NotifyTelegram:
		return "chat " + c.ChatID
	case store.NotifyDiscord:
		if i := strings.LastIndex(c.URL, "/"); i > 0 {
			return c.URL[:i] + "/…"
		}
	case store.NotifyBark:
		return "device …" + tail(c.DeviceKey)
	case store.NotifyServerChan:
		return "SendKey …" + tail(c.SendKey)
	case store.NotifyWebhook:
		if u, err := url.Parse(c.URL); err == nil {
			return u.Scheme + "://" + u.Host + "/…"
		}
	}
	return "…"
}

// ParseDigestAt reads a digest time, "HH:MM"; "" is no digest.
func ParseDigestAt(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	t, err := time.Parse("15:04", s)
	if err != nil {
		return "", errors.New("the digest time should be like 09:00")
	}
	return t.Format("15:04"), nil
}

// LastSlot is the latest digest time at or before now, in loc.
func LastSlot(digestAt string, now time.Time, loc *time.Location) time.Time {
	hh, mm, _ := strings.Cut(digestAt, ":")
	h, _ := strconv.Atoi(hh)
	m, _ := strconv.Atoi(mm)
	local := now.In(loc)
	slot := time.Date(local.Year(), local.Month(), local.Day(), h, m, 0, 0, loc)
	if slot.After(now) {
		slot = time.Date(local.Year(), local.Month(), local.Day()-1, h, m, 0, 0, loc)
	}
	return slot.UTC()
}

var (
	barkDeviceKey = regexp.MustCompile(`^[A-Za-z0-9_-]{6,120}$`)
	serverChanKey = regexp.MustCompile(`^[A-Za-z0-9_-]{8,80}$`)
)

// checkServer checks an optional base address ("https://api.day.app").
func checkServer(server string) error {
	if strings.TrimSpace(server) == "" {
		return nil
	}
	u, err := url.Parse(server)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return errors.New("the server address should be an http or https address, like https://api.day.app")
	}
	return nil
}

// serverBase is a channel's base address, or fallback when it has none.
func serverBase(server, fallback string) string {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	if server == "" {
		return fallback
	}
	if !strings.Contains(server, "://") {
		server = "https://" + server
	}
	return server
}

// tail is the last four characters of a secret, for Masked.
func tail(s string) string {
	if len(s) <= 4 {
		return "…"
	}
	return s[len(s)-4:]
}
