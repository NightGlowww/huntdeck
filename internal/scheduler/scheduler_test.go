package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/fake"
	"github.com/mdpsec/huntdeck/internal/store"
	"github.com/mdpsec/huntdeck/internal/syncer"
)

type runs map[string]*store.Run // by mode

func (r runs) LastRun(_ context.Context, _, mode string) (*store.Run, error) {
	if run, ok := r["err"]; ok && run == nil {
		return nil, errors.New("database is locked")
	}
	return r[mode], nil
}

func TestFirstRun(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) *store.Run { return &store.Run{StartedAt: now.Add(-ago), Status: store.RunOK} }
	partial := func(ago time.Duration) *store.Run {
		return &store.Run{StartedAt: now.Add(-ago), Status: store.RunPartial}
	}
	type job struct {
		mode     string
		interval time.Duration
	}
	quick := job{syncer.ModeQuick, 30 * time.Minute}
	full := job{syncer.ModeFull, 6 * time.Hour}
	web := job{syncer.ModeWeb, 24 * time.Hour}

	tests := []struct {
		name string
		job  job
		runs runs
		want time.Time
	}{
		{"never polled: quick waits a full interval", quick, runs{}, now.Add(30 * time.Minute)},
		{"never polled: full starts soon", full, runs{}, now.Add(5 * time.Second)},
		{"quick keeps its schedule across restarts", quick, runs{"quick": at(10 * time.Minute)}, now.Add(20 * time.Minute)},
		{"quick counts a recent full poll", quick, runs{"full": at(25 * time.Minute)}, now.Add(5 * time.Minute)},
		{"quick uses the later of quick and full", quick, runs{"quick": at(29 * time.Minute), "full": at(2 * time.Minute)}, now.Add(28 * time.Minute)},
		{"full ignores quick polls", full, runs{"quick": at(time.Minute), "full": at(5 * time.Hour)}, now.Add(time.Hour)},
		{"overdue runs shortly", full, runs{"full": at(7 * time.Hour)}, now.Add(5 * time.Second)},
		{"never synced: a browser sync waits for the first listing", web, runs{}, now.Add(10 * time.Minute)},
		{"a browser sync ignores polls", web, runs{"full": at(time.Hour), "web": at(20 * time.Hour)}, now.Add(4 * time.Hour)},
		{"a browser sync that didn't finish OK tries again an hour after", web, runs{"web": partial(20 * time.Minute)}, now.Add(40 * time.Minute)},
		{"… or shortly, if that's past", web, runs{"web": partial(3 * time.Hour)}, now.Add(5 * time.Second)},
		{"polls that didn't finish OK keep their interval", full, runs{"full": &store.Run{StartedAt: now.Add(-time.Hour), Status: store.RunPartial}}, now.Add(5 * time.Hour)},
		{"a run a restart cut short is retried soon", full,
			runs{"full": {StartedAt: now.Add(-time.Hour), Status: store.RunFailed, Error: store.RunInterrupted}},
			now.Add(5 * time.Second)},
		{"a run that failed keeps its interval", full,
			runs{"full": {StartedAt: now.Add(-time.Hour), Status: store.RunFailed, Error: "401 Unauthorized"}},
			now.Add(5 * time.Hour)},
		{"store error falls back to the first delay", quick, runs{"err": nil}, now.Add(30 * time.Minute)},
	}
	for _, tt := range tests {
		j := Job{Platform: fake.New(), Mode: tt.job.mode}
		got := firstRun(context.Background(), slog.New(slog.DiscardHandler), tt.runs, "fake", tt.job.mode,
			tt.job.interval, j.firstDelay(tt.job.interval), now)
		if !got.Equal(tt.want) {
			t.Errorf("%s: first run at %s, want %s", tt.name, got.Sub(now), tt.want.Sub(now))
		}
	}
}

// A failed browser login is retried after 1h, 2h, 4h… until that's no
// sooner than the sync's own interval.
func TestLoginRetry(t *testing.T) {
	day := 24 * time.Hour
	for _, tt := range []struct {
		fails    int
		interval time.Duration
		want     time.Duration // 0: the interval
	}{
		{0, day, 0},
		{1, day, time.Hour},
		{2, day, 2 * time.Hour},
		{3, day, 4 * time.Hour},
		{5, day, 16 * time.Hour},
		{6, day, 0},
		{40, day, 0},
		{1, 6 * time.Hour, time.Hour},
		{4, 6 * time.Hour, 0},
		{1, time.Hour, 0},
	} {
		d, ok := loginRetry(tt.fails, tt.interval)
		if ok != (tt.want != 0) || ok && d != tt.want {
			t.Errorf("loginRetry(%d, %s) = %s, %v; want %s", tt.fails, tt.interval, d, ok, tt.want)
		}
	}
}

// webSyncer is a platform whose browser login is in a given state.
type webSyncer struct {
	*fake.Platform
	session platform.WebSession
}

func (w webSyncer) WebSyncConfigured() bool                                      { return true }
func (w webSyncer) WebSync(context.Context, []string, platform.StatsSaver) error { return nil }
func (w webSyncer) WebSession() platform.WebSession                              { return w.session }
func (w webSyncer) WebSyncPulls() []string                                       { return nil }

// Only a browser sync whose login failed, and wasn't refused outright, is
// retried sooner.
func TestLoginFailed(t *testing.T) {
	for _, tt := range []struct {
		name string
		job  Job
		want bool
	}{
		{"the site took Chrome for a bot", Job{Mode: syncer.ModeWeb, Platform: webSyncer{fake.New(), platform.WebSession{Failure: "bot"}}}, true},
		{"signed in", Job{Mode: syncer.ModeWeb, Platform: webSyncer{fake.New(), platform.WebSession{SignedIn: true}}}, false},
		{"credentials refused", Job{Mode: syncer.ModeWeb, Platform: webSyncer{fake.New(), platform.WebSession{Failure: "wrong password", Stuck: true}}}, false},
		{"not tried yet", Job{Mode: syncer.ModeWeb, Platform: webSyncer{fake.New(), platform.WebSession{}}}, false},
		{"a full poll of the same platform", Job{Mode: syncer.ModeFull, Platform: webSyncer{fake.New(), platform.WebSession{Failure: "bot"}}}, false},
		{"a platform without a browser sync", Job{Mode: syncer.ModeWeb, Platform: fake.New()}, false},
	} {
		if got := loginFailed(tt.job); got != tt.want {
			t.Errorf("%s: loginFailed = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// A run that started after the job was scheduled (run now) pushes it back
// one interval from that run; the job's own last run, a jittered early
// timer, no run at all or a store error don't.
func TestRanSince(t *testing.T) {
	scheduled := time.Date(2026, 10, 28, 21, 27, 0, 0, time.UTC)
	at := func(after time.Duration) *store.Run { return &store.Run{StartedAt: scheduled.Add(after)} }
	day := 24 * time.Hour
	for _, tt := range []struct {
		name string
		mode string
		runs runs
		want time.Time // zero: run now
	}{
		{"manual browser sync after scheduling", syncer.ModeWeb, runs{"web": at(30 * time.Second)}, scheduled.Add(30*time.Second + day)},
		{"its own last run, before scheduling", syncer.ModeWeb, runs{"web": at(-10 * time.Minute)}, time.Time{}},
		{"never run", syncer.ModeWeb, runs{}, time.Time{}},
		{"a manual full poll pushes the quick poll", syncer.ModeQuick, runs{"full": at(time.Minute)}, scheduled.Add(time.Minute + day)},
		{"a quick poll doesn't push the full poll", syncer.ModeFull, runs{"quick": at(time.Minute)}, time.Time{}},
		{"store error runs it", syncer.ModeFull, runs{"err": nil}, time.Time{}},
	} {
		due, later := ranSince(context.Background(), tt.runs, "fake", tt.mode, day, scheduled)
		if later != !tt.want.IsZero() || !due.Equal(tt.want) {
			t.Errorf("%s: due %v (later %v), want %v", tt.name, due, later, tt.want)
		}
	}
}

func TestJitterStaysWithinFivePercent(t *testing.T) {
	const d = time.Hour
	for range 1000 {
		if j := jitter(d); j < d*95/100 || j > d*105/100 {
			t.Fatalf("jitter(%s) = %s", d, j)
		}
	}
}

type flakySession struct {
	mu    sync.Mutex
	errs  []error
	calls int
}

func (f *flakySession) KeepAlive(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	err := f.errs[min(f.calls, len(f.errs)-1)]
	f.calls++
	return err
}

func (f *flakySession) callsSoFar() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestKeepAliveRunsUntilCancelled(t *testing.T) {
	ka := &flakySession{errs: []error{nil, errors.New("session expired"), nil}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		keepAlive(ctx, slog.New(slog.DiscardHandler), ka, time.Millisecond, func() bool { return true })
		close(done)
	}()
	deadline := time.After(2 * time.Second)
	for ka.callsSoFar() < 3 {
		select {
		case <-deadline:
			t.Fatal("keepalive did not keep running after a failure")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("keepalive did not stop on cancel")
	}
}

func TestKeepAliveSkipsDisabledPlatforms(t *testing.T) {
	ka := &flakySession{errs: []error{nil}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	keepAlive(ctx, slog.New(slog.DiscardHandler), ka, time.Millisecond, func() bool { return false })
	if n := ka.callsSoFar(); n != 0 {
		t.Errorf("disabled platform kept alive %d times", n)
	}
}

func TestParseAndFormatInterval(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
		out  string
	}{
		{"15m", 15 * time.Minute, "15m"},
		{" 6H ", 6 * time.Hour, "6h"},
		{"1d", 24 * time.Hour, "1d"},
		{"1h30m", 90 * time.Minute, "1h30m"},
		{"off", 0, "off"},
		{"0", 0, "off"},
		{"", 0, "off"},
	}
	for _, tt := range tests {
		got, err := ParseInterval(tt.in)
		if err != nil || got != tt.want || FormatInterval(got) != tt.out {
			t.Errorf("ParseInterval(%q) = %s, %v (formats as %q); want %s, %q", tt.in, got, err, FormatInterval(got), tt.want, tt.out)
		}
	}
	for _, bad := range []string{"soon", "-5m", "xd", "-1d"} {
		if _, err := ParseInterval(bad); err == nil {
			t.Errorf("ParseInterval(%q) accepted", bad)
		}
	}
}

func TestSettingsValidate(t *testing.T) {
	tests := []struct {
		s    Settings
		want string // substring of the error, "" for valid
	}{
		{Settings{true, 15 * time.Minute, 6 * time.Hour}, ""},
		{Settings{false, 0, 0}, ""},
		{Settings{true, 0, 6 * time.Hour}, ""},
		{Settings{true, time.Minute, 6 * time.Hour}, "quick poll interval"},
		{Settings{true, 15 * time.Minute, 30 * time.Minute}, "full poll interval"},
		{Settings{true, 8 * 24 * time.Hour, 0}, "quick poll interval"},
		{Settings{true, 2 * time.Hour, time.Hour}, "more often"},
	}
	for _, tt := range tests {
		err := tt.s.Validate()
		if (tt.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.want)) {
			t.Errorf("Validate(%+v) = %v, want %q", tt.s, err, tt.want)
		}
	}
}

func TestScheduleSavesAndAppliesSettings(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	migs, _ := db.Migrations()
	if _, err := d.Migrate(ctx, migs, t.TempDir(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	st := store.New(d, nil)
	defaults := map[string]Settings{"hackerone": {true, 15 * time.Minute, 6 * time.Hour}, "bugcrowd": {true, 30 * time.Minute, 6 * time.Hour}}

	s, err := NewSchedule(ctx, st, defaults)
	if err != nil {
		t.Fatal(err)
	}
	changed := s.Changed()
	off := Settings{Enabled: false, Quick: 20 * time.Minute, Full: 12 * time.Hour}
	if err := s.Set(ctx, "bugcrowd", off); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	default:
		t.Error("Set did not wake the scheduler")
	}
	if got := s.Get("bugcrowd"); got != off || s.Enabled("bugcrowd") || got.Interval(syncer.ModeQuick) != 0 || !s.Overridden("bugcrowd") {
		t.Errorf("after Set: %+v", got)
	}
	if err := s.Set(ctx, "bugcrowd", Settings{true, time.Minute, time.Hour}); err == nil {
		t.Error("invalid settings saved")
	}
	if err := s.Set(ctx, "nope", off); !errors.Is(err, ErrUnknownPlatform) {
		t.Errorf("unknown platform: %v", err)
	}

	// A restart loads the saved settings.
	s2, err := NewSchedule(ctx, st, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Get("bugcrowd"); got != off {
		t.Errorf("after restart: %+v, want %+v", got, off)
	}
	if got := s2.Get("hackerone"); got != defaults["hackerone"] || s2.Overridden("hackerone") {
		t.Errorf("untouched platform: %+v", got)
	}

	if err := s2.Reset(ctx, "bugcrowd"); err != nil {
		t.Fatal(err)
	}
	s3, _ := NewSchedule(ctx, st, defaults)
	if got := s3.Get("bugcrowd"); got != defaults["bugcrowd"] || s3.Overridden("bugcrowd") {
		t.Errorf("after reset: %+v", got)
	}
}

func TestScheduleWebSync(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	migs, _ := db.Migrations()
	if _, err := d.Migrate(ctx, migs, t.TempDir(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	st := store.New(d, nil)
	defaults := map[string]Settings{"intigriti": {true, 15 * time.Minute, 6 * time.Hour}}
	s, err := NewSchedule(ctx, st, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.WebInterval("intigriti"); got != 24*time.Hour || s.WebOverridden("intigriti") {
		t.Errorf("default browser sync every %s", got)
	}
	for _, bad := range []time.Duration{time.Hour, 8 * 24 * time.Hour} {
		if err := s.SetWeb(ctx, "intigriti", store.WebSyncSettings{Enabled: true, Interval: bad}); err == nil {
			t.Errorf("browser sync every %s saved", bad)
		}
	}
	if err := s.SetWeb(ctx, "nope", store.DefaultWebSync); !errors.Is(err, ErrUnknownPlatform) {
		t.Errorf("unknown platform: %v", err)
	}
	changed := s.Changed()
	if err := s.SetWeb(ctx, "intigriti", store.WebSyncSettings{Enabled: true, Interval: 12 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	default:
		t.Error("SetWeb did not wake the scheduler")
	}
	s2, _ := NewSchedule(ctx, st, defaults)
	if got := s2.WebInterval("intigriti"); got != 12*time.Hour || !s2.WebOverridden("intigriti") {
		t.Errorf("after restart: every %s", got)
	}
	// Off, or with the platform off, it doesn't run.
	_ = s2.SetWeb(ctx, "intigriti", store.WebSyncSettings{Interval: 12 * time.Hour})
	if got := s2.WebInterval("intigriti"); got != 0 {
		t.Errorf("switched off: every %s", got)
	}
	if err := s2.ResetWeb(ctx, "intigriti"); err != nil {
		t.Fatal(err)
	}
	_ = s2.Set(ctx, "intigriti", Settings{false, 15 * time.Minute, 6 * time.Hour})
	if got := s2.WebInterval("intigriti"); got != 0 || s2.WebOverridden("intigriti") {
		t.Errorf("platform off: every %s", got)
	}
}

// A proxy that drops a connection is not the platform refusing. Five attempts hit
// EOF and a full poll then waited six hours; it should try again in minutes.
func TestTransientFailuresAreRetriedSoon(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"giving up after 5 attempts: GET https://x: EOF", true},
		{"read tcp: connection reset by peer", true},
		{"dial tcp: i/o timeout", true},
		{"hackerone: authentication failed; check H1_USERNAME and H1_TOKEN", false},
		{"HTTP 429", false},
	}
	for _, c := range cases {
		if got := transient(c.msg); got != c.want {
			t.Errorf("transient(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}
