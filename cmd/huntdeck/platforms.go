package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/bugcrowd"
	"github.com/mdpsec/huntdeck/internal/platform/custom"
	"github.com/mdpsec/huntdeck/internal/platform/hackerone"
	"github.com/mdpsec/huntdeck/internal/platform/intigriti"
	"github.com/mdpsec/huntdeck/internal/platform/yeswehack"
	"github.com/mdpsec/huntdeck/internal/scheduler"
	"github.com/mdpsec/huntdeck/internal/sessionjar"
	"github.com/mdpsec/huntdeck/internal/store"
	"github.com/mdpsec/huntdeck/internal/syncer"
	"github.com/mdpsec/huntdeck/internal/web"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// configured is a platform built from config, plus its polling settings.
// Pass the embedded Platform, not configured itself, to the syncer and
// scheduler: the wrapper hides optional interfaces such as
// platform.KeepAliver and platform.StatsFetcher.
type configured struct {
	platform.Platform
	fullInterval, quickInterval time.Duration
	concurrency                 int
}

// buildPlatforms returns every platform that has credentials configured.
// Website logins run in host's tabs.
func buildPlatforms(cfg config.Config, host *weblogin.Host) ([]configured, error) {
	var out []configured
	if h := cfg.HackerOne; h.Enabled() {
		o := hackerone.Options{
			Username: h.Username,
			Token:    h.Token.Reveal(),
			HTTP:     httpclient.New(httpclient.Options{RatePerSecond: h.RatePerSecond, Burst: 2, Proxy: cfg.Proxy}),
		}
		if h.Email != "" {
			// The website login keeps its session in this jar, saved in the
			// data directory so a restart reuses it; the login browser's
			// profile keeps the remembered login. A changed H1_EMAIL starts
			// afresh, the browser's profile too (others' logins in it are
			// remembered again at their next login).
			jarPath := filepath.Join(cfg.DataDir, "sessions", "hackerone.json")
			profile := host.Chrome().Profile
			loginState := filepath.Join(cfg.DataDir, "sessions", "hackerone-login.json")
			if _, err := sessionjar.Claim(filepath.Join(cfg.DataDir, "sessions", "hackerone-account"), h.Email,
				jarPath, profile, loginState); err != nil {
				return nil, fmt.Errorf("hackerone: %w", err)
			}
			jar, err := sessionjar.Open(jarPath, "hackerone.com")
			if err != nil {
				return nil, err
			}
			o.Email, o.Password, o.TOTPSecret = h.Email, h.Password.Reveal(), h.TOTPSecret.Reveal()
			o.Jar, o.WebHTTP = jar, httpclient.New(httpclient.Options{RatePerSecond: 1, Burst: 2, Proxy: cfg.Proxy, Jar: jar})
			o.Browser, o.LoginState = host, loginState
			host.Register(hackerone.Name, hackerone.WebHome, jar)
		}
		c, err := hackerone.New(o)
		if err != nil {
			return nil, err
		}
		out = append(out, configured{Platform: c, fullInterval: h.FullInterval, quickInterval: h.QuickInterval, concurrency: h.Concurrency})
	}
	if b := cfg.Bugcrowd; b.Enabled() {
		o := bugcrowd.Options{
			Session:    b.Session.Reveal(),
			Email:      b.Email,
			Password:   b.Password.Reveal(),
			TOTPSecret: b.TOTPSecret.Reveal(),
			HTTP:       httpclient.Options{RatePerSecond: b.RatePerSecond, Proxy: cfg.Proxy},
		}
		if b.Email != "" {
			// The login runs in the login browser; its session (bugcrowd.com,
			// the identity service and Okta's cookies) is saved so a restart
			// carries on with it. A changed BC_EMAIL starts afresh. The
			// shared browser profile isn't the account's alone, so it stays.
			dir := filepath.Join(cfg.DataDir, "sessions")
			jarPath, loginState := filepath.Join(dir, "bugcrowd.json"), filepath.Join(dir, "bugcrowd-login.json")
			if _, err := sessionjar.Claim(filepath.Join(dir, "bugcrowd-account"), b.Email, jarPath, loginState); err != nil {
				return nil, fmt.Errorf("bugcrowd: %w", err)
			}
			jar, err := sessionjar.Open(jarPath, "bugcrowd.com", "identity.bugcrowd.com", "login.hackers.bugcrowd.com")
			if err != nil {
				return nil, err
			}
			o.Browser, o.Jar, o.LoginState = host, jar, loginState
			host.Register(bugcrowd.Name, bugcrowd.WebHome, jar)
		}
		c, err := bugcrowd.New(o)
		if err != nil {
			return nil, err
		}
		out = append(out, configured{Platform: c, fullInterval: b.FullInterval, quickInterval: b.QuickInterval, concurrency: b.Concurrency})
	}
	if it := cfg.Intigriti; it.Enabled() {
		// The web login keeps its session in this jar, saved in the data
		// directory so a restart reuses it rather than logging in again;
		// redirects may go to the login site and back, and never carry the
		// API token.
		jarPath := filepath.Join(cfg.DataDir, "sessions", "intigriti.json")
		profile := host.Chrome().Profile
		loginState := filepath.Join(cfg.DataDir, "sessions", "intigriti-login.json")
		if it.Email != "" {
			// A saved session is the account's that signed in: a changed
			// IT_EMAIL starts the web login afresh.
			if _, err := sessionjar.Claim(filepath.Join(cfg.DataDir, "sessions", "intigriti-account"), it.Email,
				jarPath, profile, loginState); err != nil {
				return nil, fmt.Errorf("intigriti: %w", err)
			}
		}
		jar, err := sessionjar.Open(jarPath, "login.intigriti.com", "app.intigriti.com")
		if err != nil {
			return nil, err
		}
		c, err := intigriti.New(intigriti.Options{
			Token: it.Token.Reveal(),
			HTTP: httpclient.New(httpclient.Options{RatePerSecond: it.RatePerSecond, Burst: 2, Proxy: cfg.Proxy, Jar: jar,
				RedirectHosts: []string{"login.intigriti.com", "app.intigriti.com"}}),
			Email: it.Email, Password: it.Password.Reveal(), TOTPSecret: it.TOTPSecret.Reveal(),
			Browser: host, Jar: jar, LoginState: loginState,
		})
		if err != nil {
			return nil, err
		}
		if it.Email != "" {
			host.Register(intigriti.Name, intigriti.WebHome, jar)
		}
		out = append(out, configured{Platform: c, fullInterval: it.FullInterval, quickInterval: it.QuickInterval, concurrency: it.Concurrency})
	}
	if y := cfg.YesWeHack; y.Enabled() {
		o := yeswehack.Options{
			Email:      y.Email,
			Password:   y.Password.Reveal(),
			TOTPSecret: y.TOTPSecret.Reveal(),
			Token:      y.Token.Reveal(),
			HTTP:       httpclient.New(httpclient.Options{RatePerSecond: y.RatePerSecond, Burst: 2, Proxy: cfg.Proxy}),
		}
		if y.Email != "" {
			// The login runs in the login browser, and the web app's token is
			// saved so a restart carries on with it. A changed YWH_EMAIL
			// starts afresh (the shared profile stays, as for Bugcrowd).
			dir := filepath.Join(cfg.DataDir, "sessions")
			tokenFile, loginState := filepath.Join(dir, "yeswehack-token"), filepath.Join(dir, "yeswehack-login.json")
			if _, err := sessionjar.Claim(filepath.Join(dir, "yeswehack-account"), y.Email, tokenFile, loginState); err != nil {
				return nil, fmt.Errorf("yeswehack: %w", err)
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, err
			}
			o.Browser, o.TokenFile, o.LoginState = host, tokenFile, loginState
		}
		c, err := yeswehack.New(o)
		if err != nil {
			return nil, err
		}
		if y.Email != "" {
			host.Register(yeswehack.Name, yeswehack.WebHome, c.BrowserSession())
		}
		out = append(out, configured{Platform: c, fullInterval: y.FullInterval, quickInterval: y.QuickInterval, concurrency: y.Concurrency})
	}
	// Custom sources: platforms HuntDeck has no client for, read from a
	// document a script of the user's writes. New programs are picked up
	// every quarter of the full interval.
	for _, src := range cfg.Sources.List {
		var client *httpclient.Client
		if !filepath.IsAbs(src.Location) {
			client = httpclient.New(httpclient.Options{RatePerSecond: 1, Burst: 1, Proxy: cfg.Proxy})
		}
		c, err := custom.New(custom.Options{Name: src.Name, Location: src.Location, HTTP: client})
		if err != nil {
			return nil, err
		}
		out = append(out, configured{Platform: c, fullInterval: cfg.Sources.Interval, quickInterval: cfg.Sources.Interval / 4, concurrency: 4})
	}
	return out, nil
}

// loginBrowser is the one Chrome website logins share, a tab each,
// through the same proxy as the platforms' API calls, shown on display
// ("": a display of its own). Its profile is kept in the data directory.
func loginBrowser(cfg config.Config, display string, log *slog.Logger) *weblogin.Host {
	dir := filepath.Join(cfg.DataDir, "chrome")
	profile := filepath.Join(dir, "browser")
	// Each platform had a browser of its own until November 2026. Intigriti's
	// profile carries on as the shared one: its login page scores the
	// browser's history (reCAPTCHA). HackerOne's remembered login is
	// typed again at its next login.
	if _, err := os.Stat(profile); errors.Is(err, fs.ErrNotExist) {
		if err := os.Rename(filepath.Join(dir, "intigriti"), profile); err == nil {
			log.Info("login browser: Intigriti's profile is now the shared one")
		}
		if err := os.RemoveAll(filepath.Join(dir, "hackerone")); err != nil {
			log.Warn("login browser: old HackerOne profile not removed", "err", err)
		}
	}
	// The profile holds live platform sessions, so it is kept to this user only:
	// a renderer runs unsandboxed in a container (measured: Chrome aborts here
	// without --no-sandbox), and this directory is the part of the data
	// directory it actually writes to.
	if err := os.MkdirAll(profile, 0o700); err != nil {
		log.Warn("login browser: cannot create the profile directory", "err", err)
	} else if info, err := os.Stat(profile); err == nil && info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(profile, 0o700); err != nil { //nolint:gosec // a directory needs its execute bit; 0700 is the tightest that works
			log.Warn("login browser: profile directory is readable by others", "mode", info.Mode().Perm())
		}
	}
	log.Info("login browser", "profile", profile, "headless", cfg.ChromeHeadless,
		"sandbox", "off (a container without user namespaces aborts Chrome with it on)")
	return weblogin.NewHost(weblogin.Chrome{Path: cfg.Chrome, Headless: cfg.ChromeHeadless, Proxy: cfg.Proxy,
		TimeZone: cfg.ChromeTimezone, Profile: profile, Display: display})
}

// scheduleJobs turns configured platforms into scheduler jobs.
func scheduleJobs(ps []configured) []scheduler.Job {
	var jobs []scheduler.Job
	for _, p := range ps {
		jobs = append(jobs,
			scheduler.Job{Platform: p.Platform, Mode: syncer.ModeFull, Concurrency: p.concurrency},
			scheduler.Job{Platform: p.Platform, Mode: syncer.ModeQuick, Concurrency: p.concurrency},
		)
		if ws, ok := p.Platform.(platform.WebSyncer); ok && ws.WebSyncConfigured() {
			jobs = append(jobs, scheduler.Job{Platform: p.Platform, Mode: syncer.ModeWeb})
		}
	}
	return jobs
}

// openSchedule loads the polling settings: intervals from the environment,
// overridden by any saved in the UI.
func openSchedule(ctx context.Context, st *store.Store, ps []configured) (*scheduler.Schedule, error) {
	defaults := map[string]scheduler.Settings{}
	for _, p := range ps {
		defaults[p.Name()] = scheduler.Settings{Enabled: true, Quick: p.quickInterval, Full: p.fullInterval}
	}
	return scheduler.NewSchedule(ctx, st, defaults)
}

// platformAliases maps short names users may type to platform names.
var platformAliases = map[string]string{
	"h1": hackerone.Name, "bc": bugcrowd.Name, "it": intigriti.Name, "ywh": yeswehack.Name,
}

func canonicalPlatform(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if a, ok := platformAliases[n]; ok {
		return a
	}
	return n
}

// pickPlatforms filters configured platforms by name ("" means all).
func pickPlatforms(ps []configured, name string) ([]configured, error) {
	if name == "" {
		if len(ps) == 0 {
			return nil, fmt.Errorf("no platforms configured; add credentials for at least one platform (see .env.example)")
		}
		return ps, nil
	}
	want := canonicalPlatform(name)
	for _, p := range ps {
		if p.Name() == want {
			return []configured{p}, nil
		}
	}
	return nil, fmt.Errorf("platform %q is not configured", name)
}

// heldPlatforms is a read-only copy's platforms: those its seed has
// programs of (it has no credentials), in the order a full install lists
// them, then any others by name.
func heldPlatforms(ctx context.Context, s *store.Store) ([]web.PlatformInfo, error) {
	held, err := s.HeldPlatforms(ctx)
	if err != nil {
		return nil, err
	}
	rank := func(name string) int {
		if i := slices.Index([]string{hackerone.Name, bugcrowd.Name, intigriti.Name, yeswehack.Name}, name); i >= 0 {
			return i
		}
		return 99
	}
	slices.SortStableFunc(held, func(a, b store.HeldPlatform) int { return cmp.Compare(rank(a.Name), rank(b.Name)) })
	out := make([]web.PlatformInfo, 0, len(held))
	for _, h := range held {
		if h.Name == store.Independent {
			continue // not a platform: lists add it as a filter where it has programs
		}
		out = append(out, web.PlatformInfo{Name: h.Name, Discloses: h.Discloses})
	}
	return out, nil
}
