package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/fake"
	"github.com/mdpsec/huntdeck/internal/platform/independent"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
	"github.com/mdpsec/huntdeck/internal/syncer"
)

// sink is a stand-in chat service or webhook that records what it gets
// and answers with the next queued status (200 when none is queued).
type sink struct {
	mu      sync.Mutex
	srv     *httptest.Server
	bodies  []string
	headers []http.Header
	paths   []string
	replies []int
}

func newSink(t *testing.T) *sink {
	s := &sink{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		status := http.StatusOK
		if len(s.replies) > 0 {
			status, s.replies = s.replies[0], s.replies[1:]
		}
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "600")
		}
		if status == http.StatusOK {
			s.bodies, s.headers, s.paths = append(s.bodies, string(b)), append(s.headers, r.Header.Clone()), append(s.paths, r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *sink) got() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bodies...)
}

func (s *sink) reply(codes ...int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replies = append(s.replies, codes...)
}

type env struct {
	t     *testing.T
	clock time.Time
	store *store.Store
	sync  *syncer.Syncer
	fake  *fake.Platform
	n     *Notifier
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	if _, err := d.Migrate(ctx, migs, t.TempDir(), log); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, clock: time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC), fake: fake.New()}
	e.store = store.New(d, func() time.Time { return e.clock })
	e.sync = syncer.New(e.store, log, 2)
	e.n = New(e.store, log, "UTC")
	return e
}

func (e *env) poll() store.Run {
	e.t.Helper()
	e.clock = e.clock.Add(time.Minute)
	run, err := e.sync.Poll(context.Background(), e.fake, syncer.Options{Mode: syncer.ModeFull, Trigger: "manual"})
	if err != nil && run.Status != store.RunFailed {
		e.t.Fatal(err)
	}
	return run
}

func (e *env) tick() {
	e.t.Helper()
	if err := e.n.Tick(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) channel(name, kind string, cfg Config, rules Rules, instant bool, digestAt string) int64 {
	e.t.Helper()
	c := store.NotifyChannel{Name: name, Kind: kind, Config: cfg.JSON(), Rules: rules.JSON(), Enabled: true, Instant: instant, DigestAt: digestAt}
	if digestAt != "" {
		slot := LastSlot(digestAt, e.clock, time.UTC)
		c.DigestSentAt = &slot
	}
	id, err := e.store.SaveNotifyChannel(context.Background(), c)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func prog(handle string, private bool) platform.Program {
	return platform.Program{Handle: handle, Name: strings.ToUpper(handle[:1]) + handle[1:], URL: "https://platform.example/" + handle,
		Private: private, State: platform.StateOpen, OffersBounty: true}
}

func entry(id, ident string) platform.ScopeEntry {
	at := scope.AssetURL
	if strings.HasPrefix(ident, "*.") {
		at = scope.AssetWildcard
	}
	return platform.ScopeEntry{PlatformID: id, InScope: true, AssetType: at, Identifier: ident, BountyEligible: true}
}

func TestPollsAreDelivered(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	discord, tg, hook := newSink(t), newSink(t), newSink(t)
	e.n.TelegramAPI = tg.srv.URL
	e.channel("discord", store.NotifyDiscord, Config{URL: discord.srv.URL + "/api/webhooks/1/tok"}, DefaultRules(), true, "")
	e.channel("phone", store.NotifyTelegram, Config{Token: "123:secret-token", ChatID: "42"}, Rules{Events: []string{EventInvite}}, true, "")
	e.channel("hook", store.NotifyWebhook, Config{URL: hook.srv.URL + "/in", Secret: "s3cret"}, DefaultRules(), true, "")
	if err := e.store.Write(ctx, func(tx *store.Tx) error {
		return tx.PutSetting(ctx, BaseURLSetting, "https://huntdeck.example/")
	}); err != nil {
		t.Fatal(err)
	}

	e.fake.SetPrograms(prog("acme", false))
	e.fake.SetScope("acme", entry("1", "*.acme.example"))
	e.poll() // the first import is a baseline: nothing to tell
	e.tick()
	if len(discord.got())+len(tg.got())+len(hook.got()) != 0 {
		t.Fatalf("the first import notified")
	}

	e.fake.SetPrograms(prog("acme", false), prog("secret", true))
	e.fake.SetScope("acme", entry("1", "*.acme.example"), entry("2", "*.new-root.example"), entry("3", "https://app.acme.example"))
	e.fake.SetScope("secret", entry("4", "app.secret.example"))
	e.poll()
	e.tick()
	d := discord.got()
	if len(d) != 1 {
		t.Fatalf("discord got %d messages", len(d))
	}
	var msg struct {
		Content  string `json:"content"`
		Mentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}
	if err := json.Unmarshal([]byte(d[0]), &msg); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"🔒 **New private invite** · fake", "[**Secret**](https://huntdeck.example/programs/fake/secret)",
		"🎯 **2 new in-scope assets**", "• `*.new-root.example` (wildcard, new root domain)", "• `https://app.acme.example`"} {
		if !strings.Contains(msg.Content, want) {
			t.Errorf("discord message lacks %q:\n%s", want, msg.Content)
		}
	}
	if msg.Mentions.Parse == nil || len(msg.Mentions.Parse) != 0 {
		t.Errorf("mentions aren't switched off: %s", d[0])
	}
	if got := tg.got(); len(got) != 1 || !strings.Contains(got[0], `"chat_id":"42"`) || !strings.Contains(got[0], "New private invite") ||
		strings.Contains(got[0], "new in-scope") || tg.paths[0] != "/bot123:secret-token/sendMessage" {
		t.Errorf("telegram got %q at %v", got, tg.paths)
	}
	h := hook.got()
	if len(h) != 1 {
		t.Fatalf("webhook got %d", len(h))
	}
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte(h[0]))
	if hook.headers[0].Get("X-Huntdeck-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) || hook.headers[0].Get("X-Huntdeck-Event") != "poll" {
		t.Errorf("webhook headers = %v", hook.headers[0])
	}
	var payload struct {
		Version int     `json:"version"`
		Type    string  `json:"type"`
		Events  []Event `json:"events"`
	}
	if err := json.Unmarshal([]byte(h[0]), &payload); err != nil || payload.Version != 1 || payload.Type != "poll" || len(payload.Events) != 2 ||
		payload.Events[0].Kind != EventInvite || payload.Events[0].Program.HuntDeckURL != "https://huntdeck.example/programs/fake/secret" ||
		payload.Events[1].Kind != EventScopeAdded || len(payload.Events[1].Assets) != 2 {
		t.Errorf("webhook payload = %+v, %v", payload, err)
	}

	e.tick()
	if len(discord.got()) != 1 || len(hook.got()) != 1 {
		t.Error("a second tick sent again")
	}

	// A failed send is retried later, after Retry-After when the service
	// asks; nothing is lost or repeated.
	discord.reply(http.StatusInternalServerError, http.StatusTooManyRequests)
	e.fake.SetScope("acme", entry("1", "*.acme.example"), entry("2", "*.new-root.example"), entry("3", "https://app.acme.example"),
		entry("5", "*.third.example"))
	e.poll()
	e.tick() // 500
	if len(discord.got()) != 1 {
		t.Fatal("sent despite the error")
	}
	e.tick()
	if len(discord.got()) != 1 {
		t.Fatal("retried before the backoff")
	}
	e.clock = e.clock.Add(2 * time.Minute)
	e.tick() // 429, Retry-After 600
	e.clock = e.clock.Add(5 * time.Minute)
	e.tick()
	if len(discord.got()) != 1 {
		t.Fatal("retried before Retry-After")
	}
	e.clock = e.clock.Add(10 * time.Minute)
	e.tick()
	if got := discord.got(); len(got) != 2 || !strings.Contains(got[1], "*.third.example") {
		t.Fatalf("after retries discord got %d", len(got))
	}

	// A refused address is given up on at once and shown in Settings.
	hook.reply(http.StatusNotFound)
	e.fake.SetScope("acme", entry("1", "*.acme.example"), entry("6", "*.fourth.example"))
	e.poll()
	e.tick()
	chans, err := e.store.NotifyChannels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chans {
		if c.Name == "hook" && (c.LastFailedAt == nil || !strings.HasPrefix(c.LastError, "HTTP 404") || c.Queued != 0) {
			t.Errorf("hook status = %+v", c)
		}
		if strings.Contains(c.LastError, "secret-token") || strings.Contains(c.LastError, "127.0.0.1") {
			t.Errorf("an error shows the channel's address: %q", c.LastError)
		}
	}
}

func TestDigestsAndPlatformAlerts(t *testing.T) {
	e := newEnv(t)
	hook, alerts := newSink(t), newSink(t)
	e.channel("digest", store.NotifyWebhook, Config{URL: hook.srv.URL}, DefaultRules(), false, "09:00")
	e.channel("alerts", store.NotifyWebhook, Config{URL: alerts.srv.URL}, Rules{Events: []string{EventPlatformProblem}}, true, "")

	e.fake.SetPrograms(prog("acme", false))
	e.fake.SetScope("acme", entry("1", "*.acme.example"))
	e.poll()
	entries := []platform.ScopeEntry{entry("1", "*.acme.example")}
	for i, ident := range []string{"*.a.example", "*.b.example"} {
		entries = append(entries, entry(string(rune('2'+i)), ident))
		e.fake.SetScope("acme", entries...)
		e.poll()
		e.tick()
	}
	if len(hook.got()) != 0 {
		t.Fatal("a digest-only channel got a message before its time")
	}
	e.clock = time.Date(2026, 10, 27, 9, 0, 30, 0, time.UTC)
	e.tick()
	e.tick()
	got := hook.got()
	if len(got) != 1 {
		t.Fatalf("digest messages = %d", len(got))
	}
	var payload struct {
		Type   string  `json:"type"`
		Events []Event `json:"events"`
	}
	if err := json.Unmarshal([]byte(got[0]), &payload); err != nil || payload.Type != "digest" || len(payload.Events) != 1 ||
		payload.Events[0].count() != 2 {
		t.Errorf("digest = %+v (merged events: %d), %v", payload.Type, len(payload.Events), err)
	}

	// Three failed polls in a row alert once; the next good one says so.
	e.fake.FailListing(errors.New("login failed"), false)
	for range 4 {
		e.poll()
		e.tick()
	}
	e.fake.FailListing(nil, false)
	e.poll()
	e.tick()
	a := alerts.got()
	if len(a) != 2 || !strings.Contains(a[0], `"kind":"platform_problem"`) || !strings.Contains(a[0], "login failed") ||
		!strings.Contains(a[1], `"kind":"platform_recovered"`) {
		t.Errorf("alerts = %q", a)
	}
}

// Saving programs added by hand notifies nothing: it's the user's own edit.
func TestOwnEditsAreNotNotified(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	discord := newSink(t)
	e.channel("discord", store.NotifyDiscord, Config{URL: discord.srv.URL + "/api/webhooks/1/tok"}, DefaultRules(), true, "")
	own := func(handle string, scope ...string) independent.Program {
		p := independent.Program{Program: platform.Program{Handle: handle, Name: handle, URL: "https://example.com/" + handle,
			State: platform.StateOpen, OffersBounty: true}}
		p.Scope, _ = independent.ParseScope(strings.Join(scope, "\n"), "", true)
		return p
	}
	for _, listing := range [][]independent.Program{
		{own("a", "*.a.example")},
		{own("a", "*.a.example", "*.new.example"), own("b", "b.example")}, // a program and scope added
		{own("b", "b.example")}, // one removed
	} {
		e.clock = e.clock.Add(time.Minute)
		if err := e.sync.SaveOwn(ctx, listing); err != nil {
			t.Fatal(err)
		}
		e.tick()
	}
	if got := discord.got(); len(got) != 0 {
		t.Errorf("own edits notified: %q", got)
	}
	runs, err := e.store.UnnotifiedRuns(ctx)
	if err != nil || len(runs) != 0 {
		t.Errorf("runs left to notify: %v, %v", runs, err)
	}
}

// Bark takes a JSON POST to {server}/push with the device key and the text;
// ServerChan takes a form POST to {server}/{SendKey}.send with title and desp.
func TestBarkAndServerChanSends(t *testing.T) {
	sink := newSink(t)
	n := &Notifier{Client: sink.srv.Client(), Log: slog.New(slog.DiscardHandler)}
	ctx := context.Background()

	if err := n.sendBark(ctx, Config{Server: sink.srv.URL, DeviceKey: "aBcD123456"}, "hello bark"); err != nil {
		t.Fatalf("bark: %v", err)
	}
	if got := sink.paths[0]; got != "/push" {
		t.Errorf("bark path = %q", got)
	}
	var barkBody map[string]string
	if err := json.Unmarshal([]byte(sink.got()[0]), &barkBody); err != nil {
		t.Fatal(err)
	}
	if barkBody["device_key"] != "aBcD123456" || barkBody["body"] != "hello bark" || barkBody["title"] != "HuntDeck" {
		t.Errorf("bark body = %v", barkBody)
	}
	if barkBody["group"] != "HuntDeck" {
		t.Errorf("bark group defaults to %q", barkBody["group"])
	}
	if err := n.sendBark(ctx, Config{Server: sink.srv.URL, DeviceKey: "aBcD123456", Group: "Acme", Sound: "alarm", Level: "timeSensitive"}, "second"); err != nil {
		t.Fatalf("bark with a group: %v", err)
	}
	var withGroup map[string]string
	if err := json.Unmarshal([]byte(sink.got()[1]), &withGroup); err != nil {
		t.Fatal(err)
	}
	if withGroup["group"] != "Acme" {
		t.Errorf("bark group = %q", withGroup["group"])
	}
	if withGroup["sound"] != "alarm" || withGroup["level"] != "timeSensitive" {
		t.Errorf("bark sound/level = %q/%q", withGroup["sound"], withGroup["level"])
	}

	if err := n.sendServerChan(ctx, Config{Server: sink.srv.URL, SendKey: "SCT123456Tabcdef"}, "hello wechat"); err != nil {
		t.Fatalf("serverchan: %v", err)
	}
	if got := sink.paths[2]; got != "/SCT123456Tabcdef.send" {
		t.Errorf("serverchan path = %q", got)
	}
	if ct := sink.headers[2].Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		t.Errorf("serverchan content type = %q", ct)
	}
	if form, err := url.ParseQuery(sink.got()[2]); err != nil || form.Get("desp") != "hello wechat" || form.Get("title") != "HuntDeck" {
		t.Errorf("serverchan form = %v (%v)", form, err)
	}
	if form, _ := url.ParseQuery(sink.got()[2]); form.Get("tags") != "HuntDeck" {
		t.Errorf("serverchan tags = %q", form.Get("tags"))
	}
}

func TestBarkAndServerChanValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		kind string
		ok   bool
	}{
		{"bark", Config{DeviceKey: "aBcD123456"}, store.NotifyBark, true},
		{"bark with server", Config{DeviceKey: "aBcD123456", Server: "https://bark.example.com"}, store.NotifyBark, true},
		{"bark without key", Config{}, store.NotifyBark, false},
		{"bark with a slashy key", Config{DeviceKey: "a/b"}, store.NotifyBark, false},
		{"bark with a bad server", Config{DeviceKey: "aBcD123456", Server: "ftp://x"}, store.NotifyBark, false},
		{"bark with a bad level", Config{DeviceKey: "aBcD123456", Level: "loud"}, store.NotifyBark, false},
		{"bark with a good level", Config{DeviceKey: "aBcD123456", Level: "passive"}, store.NotifyBark, true},
		{"serverchan", Config{SendKey: "SCT123456Tabcdef"}, store.NotifyServerChan, true},
		{"serverchan without key", Config{}, store.NotifyServerChan, false},
	}
	for _, c := range cases {
		if err := c.cfg.Validate(c.kind); (err == nil) != c.ok {
			t.Errorf("%s: Validate = %v, want ok=%v", c.name, err, c.ok)
		}
	}
	if got := (Config{DeviceKey: "aBcD123456"}).Masked(store.NotifyBark); !strings.HasSuffix(got, "3456") || strings.Contains(got, "aBcD") {
		t.Errorf("bark masked = %q", got)
	}
}

// A poll's change reaches a Bark device and a ServerChan channel through the
// same outbox as the other kinds.
func TestBarkAndServerChanDeliveries(t *testing.T) {
	e := newEnv(t)
	bark, sc := newSink(t), newSink(t)
	e.channel("phone", store.NotifyBark, Config{Server: bark.srv.URL, DeviceKey: "aBcD123456"}, DefaultRules(), true, "")
	e.channel("wechat", store.NotifyServerChan, Config{Server: sc.srv.URL, SendKey: "SCT123456Tabcdef"}, DefaultRules(), true, "")

	e.fake.SetPrograms(prog("acme", false))
	e.fake.SetScope("acme", entry("1", "*.acme.example"))
	e.poll() // the first import is a baseline: nothing to tell
	e.tick()
	if len(bark.got())+len(sc.got()) != 0 {
		t.Fatal("the first import notified")
	}

	e.fake.SetPrograms(prog("acme", false), prog("secret", true))
	e.fake.SetScope("acme", entry("1", "*.acme.example"), entry("2", "*.new-root.example"))
	e.fake.SetScope("secret", entry("3", "app.secret.example"))
	e.poll()
	e.tick()

	if len(bark.got()) == 0 {
		t.Fatal("bark got nothing")
	}
	if bark.paths[0] != "/push" {
		t.Errorf("bark path = %q", bark.paths[0])
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(bark.got()[0]), &body); err != nil {
		t.Fatal(err)
	}
	if body["device_key"] != "aBcD123456" || !strings.Contains(body["body"], "new in-scope") {
		t.Errorf("bark body = %q", bark.got()[0])
	}

	if len(sc.got()) == 0 {
		t.Fatal("serverchan got nothing")
	}
	if !strings.HasSuffix(sc.paths[0], "/SCT123456Tabcdef.send") {
		t.Errorf("serverchan path = %q", sc.paths[0])
	}
	form, err := url.ParseQuery(sc.got()[0])
	if err != nil || !strings.Contains(form.Get("desp"), "new in-scope") {
		t.Errorf("serverchan desp = %q", form.Get("desp"))
	}
}
