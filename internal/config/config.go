// Package config loads HuntDeck configuration from environment variables.
//
// Every variable can instead be read from a file by setting NAME_FILE to a
// path (the Docker secrets convention). Setting both NAME and NAME_FILE is an
// error, so a stale value can't silently win.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config is the fully resolved runtime configuration.
type Config struct {
	Listen    string     // HUNTDECK_LISTEN, host:port for the HTTP server
	DataDir   string     // HUNTDECK_DATA_DIR, holds the database and backups
	LogLevel  slog.Level // HUNTDECK_LOG_LEVEL: debug, info, warn, error
	LogFormat string     // HUNTDECK_LOG_FORMAT: text or json
	Proxy     *url.URL   // HUNTDECK_PROXY, optional proxy for platform traffic and the login browser
	// HUNTDECK_CHROME: the Chrome or Chromium for logins that need a browser
	// (Intigriti's web login); found on PATH if unset. It runs windowed, on a
	// virtual display without one, unless HUNTDECK_CHROME_HEADLESS is set.
	Chrome         string
	ChromeHeadless bool
	// HUNTDECK_CHROME_TIMEZONE: the browser's clock, which should match the
	// proxy's country (default: HUNTDECK_TIMEZONE).
	ChromeTimezone string
	// HUNTDECK_VNC: keep one virtual display for the login browsers, with
	// a VNC server on it, and serve noVNC at /novnc/ to watch and take
	// them over. Off by default.
	VNC bool
	// HUNTDECK_NOVNC_DIR: where the noVNC web client is (the image has it
	// in /usr/share/novnc).
	NoVNCDir string
	// HUNTDECK_READ_ONLY: a read-only install for showing other people,
	// such as a demo loaded from a seed: nothing can be changed through the
	// web UI or API, Settings and invites aren't served, and it never
	// polls. It refuses to start with platform credentials, VNC or a proxy
	// set, so it can't hold anything of an account's.
	ReadOnly bool
	// HUNTDECK_SEED_URL: where a new install downloads its starter seed
	// from, once, on its first start with an empty database (DefaultSeedURL
	// unless set; "off" for none). Never fetched again once the database
	// holds programs, nor by a read-only copy, which loads its own.
	SeedURL string

	// HUNTDECK_ALLOWED_HOSTS: hostnames the web UI may be reached by, besides
	// IP addresses and localhost (guards against DNS rebinding).
	AllowedHosts []string
	// HUNTDECK_TIMEZONE: the IANA time zone dates and times are shown in,
	// until one is chosen in Settings. Data is always stored in UTC.
	Timezone string
	// HUNTDECK_LANGUAGE: the interface language when a visitor hasn't picked
	// one: "auto" (the browser's, the default) or a language with a catalog,
	// such as "zh".
	Language string
	// HUNTDECK_PASSWORD: when set, HuntDeck asks for it before showing
	// anything: one password for the whole install, still no user accounts.
	// Changing it signs every browser out.
	Password Secret
	// HUNTDECK_FEEDBACK_EMAIL: where the UI's "Send feedback" links mail
	// to (a plain mailto:, nothing is sent by HuntDeck); none if empty.
	FeedbackEmail string
	// HUNTDECK_AI_BASE_URL, HUNTDECK_AI_MODEL, HUNTDECK_AI_KEY: an
	// OpenAI-compatible endpoint for optional features. Unset means no AI:
	// every page works without it (docs/design/ai-layer.md).
	AIBaseURL string
	AIModel   string
	AIKey     Secret
	// POJIA indexes publicly disclosed bug bounty reports; the key is for its
	// stateless MCP endpoint.
	PojiaURL string
	PojiaKey Secret
	// ScanURL and ScanKey are a ScopeSentry instance: whose MCP endpoint takes
	// the targets HuntDeck finds and scans them.
	ScanURL string
	ScanKey Secret
	// ScanReadBatch is how many tasks one reconcile pass asks the scanner about.
	// Its MCP allows about ten calls a minute, so eight fits; a self-hosted
	// instance with a higher limit can raise it, and a shared one lower it.
	ScanReadBatch int
	// JevURL, JevKey and JevModel are TypeSafe's System One model: a typed,
	// calibrated judgement in a few hundred milliseconds, asked before a
	// consequential action.
	JevURL   string
	JevKey   Secret
	JevModel string
	// JevProxy is an HTTP proxy the decision call leaves through, when the
	// host is geo-blocked on a direct egress.
	JevProxy string
	// JevProvider is which decision service answers: typesafe (default),
	// bailian, cloudflare or openai. The three non-OpenAI providers speak the
	// same protocol, so only the address and the credentials differ.
	JevProvider string
	// JevAccountID is Cloudflare's account and JevWorkspaceID is Bailian's.
	JevAccountID   string
	JevWorkspaceID string
	// HUNTDECK_CLIENT_IP_HEADER: the header a proxy in front puts the
	// visitor's address in (CF-Connecting-IP behind Cloudflare), for a
	// read-only copy's per-visitor limits. Set it only when every request
	// comes through that proxy, or visitors can choose their own address.
	ClientIPHeader string
	// HUNTDECK_PUBLIC_URL: where a read-only copy is published, such as
	// https://huntdeck.io. With it the copy may be indexed by search
	// engines (its API isn't) and its pages carry canonical links, a
	// sitemap and link previews. A full install is never indexed.
	PublicURL string
	// HUNTDECK_X_ACCOUNT: an X (Twitter) account to follow for news, such
	// as huntdeck: a read-only copy links to it, and its link previews
	// name it. None if empty.
	XAccount string
	// RepoURL is HuntDeck's source repository (HUNTDECK_REPO_URL), such as
	// https://github.com/mdpsec/huntdeck: a read-only copy links to it so
	// visitors can run their own. None if empty.
	RepoURL string

	HackerOne HackerOne
	Bugcrowd  Bugcrowd
	Intigriti Intigriti
	YesWeHack YesWeHack
	VulBox    VulBox
	IssueHunt IssueHunt
	GObugfree GObugfree
	Inspectiv Inspectiv
	// HuntMail is the user's HuntMail inbox, read by the Email page.
	HuntMail HuntMail
	// HUNTDECK_SOURCES: platforms HuntDeck has no client for, read from
	// documents a script of the user's writes (sources.go).
	Sources Sources
}

// HackerOne holds HackerOne credentials and polling settings.
type HackerOne struct {
	Username string // H1_USERNAME
	Token    Secret // H1_TOKEN
	// H1_EMAIL, H1_PASSWORD and H1_TOTP_SECRET sign in to the website, for
	// private programs' stats and pending invitations; the login runs in
	// Chrome. H1_OTP is taken for H1_TOTP_SECRET too.
	Email         string
	Password      Secret
	TOTPSecret    Secret
	FullInterval  time.Duration // H1_FULL_INTERVAL: refresh every program's scope; 0 disables
	QuickInterval time.Duration // H1_QUICK_INTERVAL: list programs, fetch new ones only; 0 disables
	RatePerSecond float64       // H1_RATE: request budget (API allows 10/s)
	Concurrency   int           // H1_CONCURRENCY: parallel scope fetches
}

// Enabled reports whether HackerOne credentials are configured.
func (h HackerOne) Enabled() bool { return h.Username != "" && h.Token != "" }

// Bugcrowd holds Bugcrowd credentials and polling settings. With
// PublicOnly and no credentials, only public programs are polled.
type Bugcrowd struct {
	Email         string        // BC_EMAIL
	Password      Secret        // BC_PASSWORD
	TOTPSecret    Secret        // BC_TOTP_SECRET: base32 key of the authenticator app
	Session       Secret        // BC_SESSION: session cookie copied from a browser
	PublicOnly    bool          // BC_PUBLIC_ONLY: poll public programs without credentials
	FullInterval  time.Duration // BC_FULL_INTERVAL
	QuickInterval time.Duration // BC_QUICK_INTERVAL
	RatePerSecond float64       // BC_RATE: Bugcrowd's WAF blocks fast clients
	Concurrency   int           // BC_CONCURRENCY
}

// Enabled reports whether Bugcrowd should be polled.
func (b Bugcrowd) Enabled() bool { return b.Email != "" || b.Session != "" || b.PublicOnly }

// Intigriti holds the Intigriti API token and polling settings.
type Intigriti struct {
	Token Secret // IT_TOKEN: personal access token
	// IT_EMAIL, IT_PASSWORD and IT_TOTP_SECRET sign in to the web app, which
	// has program stats the API lacks; the first login runs in Chrome.
	Email         string
	Password      Secret
	TOTPSecret    Secret
	FullInterval  time.Duration // IT_FULL_INTERVAL
	QuickInterval time.Duration // IT_QUICK_INTERVAL
	RatePerSecond float64       // IT_RATE
	Concurrency   int           // IT_CONCURRENCY
}

// Enabled reports whether Intigriti should be polled.
func (i Intigriti) Enabled() bool { return i.Token != "" }

// YesWeHack holds YesWeHack credentials and polling settings. With
// PublicOnly and no credentials, only public programs are polled.
type YesWeHack struct {
	Email         string        // YWH_EMAIL
	Password      Secret        // YWH_PASSWORD
	TOTPSecret    Secret        // YWH_TOTP_SECRET
	Token         Secret        // YWH_TOKEN: bearer token copied from a browser (lasts hours)
	PublicOnly    bool          // YWH_PUBLIC_ONLY
	FullInterval  time.Duration // YWH_FULL_INTERVAL
	QuickInterval time.Duration // YWH_QUICK_INTERVAL
	RatePerSecond float64       // YWH_RATE
	Concurrency   int           // YWH_CONCURRENCY
}

// Enabled reports whether YesWeHack should be polled.
func (y YesWeHack) Enabled() bool { return y.Email != "" || y.Token != "" || y.PublicOnly }

// VulBox holds VulBox (漏洞盒子) credentials and polling settings. With
// PublicOnly and no credentials, only public projects are polled.
type VulBox struct {
	Account       string        // VB_ACCOUNT: username, phone or email
	Password      Secret        // VB_PASSWORD
	TOTPSecret    Secret        // VB_TOTP_SECRET: optional 6-digit authenticator secret
	Session       Secret        // VB_SESSION: optional vulbox_token / Bearer / Cookie header
	AutoJoin      bool          // VB_AUTO_JOIN: auto-confirm/join non-margin projects & agreements (default true)
	PublicOnly    bool          // VB_PUBLIC_ONLY: poll public projects without credentials
	FullInterval  time.Duration // VB_FULL_INTERVAL
	QuickInterval time.Duration // VB_QUICK_INTERVAL
	Rate          float64       // VB_RATE
	Concurrency   int           // VB_CONCURRENCY
}

// Enabled reports whether VulBox should be polled.
func (v VulBox) Enabled() bool { return v.Account != "" || v.Session != "" || v.PublicOnly }

// IssueHunt holds IssueHunt credentials and polling settings. With
// PublicOnly and no API key, only public programs are polled.
type IssueHunt struct {
	APIKey        Secret        // IH_API_KEY: User API key (or web bearer JWT)
	PublicOnly    bool          // IH_PUBLIC_ONLY: poll public programs without an API key
	FullInterval  time.Duration // IH_FULL_INTERVAL
	QuickInterval time.Duration // IH_QUICK_INTERVAL
	Rate          float64       // IH_RATE
	Concurrency   int           // IH_CONCURRENCY
}

// Enabled reports whether IssueHunt should be polled.
func (i IssueHunt) Enabled() bool { return i.APIKey != "" || i.PublicOnly }

// GObugfree holds GObugfree credentials and polling settings. With
// PublicOnly and no credentials, only public programs are polled.
type GObugfree struct {
	Email         string        // GBF_EMAIL
	Password      Secret        // GBF_PASSWORD
	TOTPSecret    Secret        // GBF_TOTP_SECRET: optional 6-digit authenticator secret
	Token         Secret        // GBF_TOKEN: optional Bearer JWT / OAuth refresh token / session cookie
	PublicOnly    bool          // GBF_PUBLIC_ONLY: poll public programs without credentials
	FullInterval  time.Duration // GBF_FULL_INTERVAL
	QuickInterval time.Duration // GBF_QUICK_INTERVAL
	Rate          float64       // GBF_RATE
	Concurrency   int           // GBF_CONCURRENCY
}

// Enabled reports whether GObugfree should be polled.
func (g GObugfree) Enabled() bool { return g.Email != "" || g.Token != "" || g.PublicOnly }

// Inspectiv holds Inspectiv credentials and polling settings. Inspectiv has
// no public listing: an account or token is required.
type Inspectiv struct {
	Email         string        // INS_EMAIL
	Password      Secret        // INS_PASSWORD
	Token         Secret        // INS_TOKEN: raw 40-hex DRF token, or "Token <hex>"
	FullInterval  time.Duration // INS_FULL_INTERVAL
	QuickInterval time.Duration // INS_QUICK_INTERVAL
	Rate          float64       // INS_RATE
	Concurrency   int           // INS_CONCURRENCY
}

// Enabled reports whether Inspectiv should be polled.
func (i Inspectiv) Enabled() bool { return i.Email != "" || i.Token != "" }

// HuntMail is the user's HuntMail inbox: a catch-all for hunting on their
// own Cloudflare account (https://github.com/mdpsec/huntmail). Its address
// holds a secret base path, so it's kept as a Secret like the key.
type HuntMail struct {
	URL Secret // HUNTMAIL_URL
	Key Secret // HUNTMAIL_API_KEY
	// HUNTMAIL_DOMAINS: domains to make new addresses on; HuntMail's own
	// list when empty.
	Domains []string
}

// Enabled reports whether a HuntMail inbox is set up.
func (h HuntMail) Enabled() bool { return h.URL != "" && h.Key != "" }

// Secret is a string that renders as [REDACTED] when printed or logged.
type Secret string

// Reveal returns the secret value. Call it only where the value is sent.
func (s Secret) Reveal() string { return string(s) }

func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return "[REDACTED]"
}

// GoString keeps %#v from printing the value.
func (s Secret) GoString() string { return s.String() }

// LogValue keeps slog from printing the value.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

// DBPath is the location of the SQLite database inside DataDir.
func (c Config) DBPath() string { return filepath.Join(c.DataDir, "huntdeck.db") }

// BackupDir is where pre-migration backups are written.
func (c Config) BackupDir() string { return filepath.Join(c.DataDir, "backups") }

// Defaults used when a variable is unset.
const (
	DefaultListen    = "0.0.0.0:8080"
	DefaultDataDir   = "/data"
	DefaultLogFormat = "text"
	DefaultTimezone  = "America/New_York"
	// DefaultSeedURL is the public starter seed, refreshed daily: public
	// programs' platform data and history only (a signed-out seed).
	DefaultSeedURL = "https://github.com/mdpsec/huntdeck/releases/download/seed/seed.db.gz"
)

// Source abstracts environment and file access so Load is testable.
type Source struct {
	LookupEnv func(string) (string, bool)
	ReadFile  func(string) ([]byte, error)
}

// OSSource reads from the real process environment and filesystem.
var OSSource = Source{LookupEnv: os.LookupEnv, ReadFile: os.ReadFile}

// Load resolves the configuration, applying defaults and validating values.
// All problems are reported together.
func Load(src Source) (Config, error) {
	r := resolver{src: src}
	cfg := Config{
		Listen:    r.get("HUNTDECK_LISTEN", DefaultListen),
		DataDir:   r.get("HUNTDECK_DATA_DIR", DefaultDataDir),
		LogFormat: strings.ToLower(r.get("HUNTDECK_LOG_FORMAT", DefaultLogFormat)),
		Timezone:  r.get("HUNTDECK_TIMEZONE", DefaultTimezone),
	}
	if !ValidTimezone(cfg.Timezone) {
		r.errs = append(r.errs, fmt.Errorf("HUNTDECK_TIMEZONE %q: not a time zone name like Australia/Brisbane or UTC", cfg.Timezone))
	}
	if cfg.ClientIPHeader = r.get("HUNTDECK_CLIENT_IP_HEADER", ""); cfg.ClientIPHeader != "" && !validHeaderName(cfg.ClientIPHeader) {
		r.errs = append(r.errs, fmt.Errorf("HUNTDECK_CLIENT_IP_HEADER %q: not a header name like CF-Connecting-IP", cfg.ClientIPHeader))
	}
	cfg.Language = strings.ToLower(strings.TrimSpace(r.get("HUNTDECK_LANGUAGE", "auto")))
	if cfg.Language != "auto" && (len(cfg.Language) < 2 || len(cfg.Language) > 8) {
		r.errs = append(r.errs, fmt.Errorf("HUNTDECK_LANGUAGE %q: want auto, en or a language like zh", cfg.Language))
	}
	cfg.AIBaseURL = strings.TrimSpace(r.get("HUNTDECK_AI_BASE_URL", ""))
	cfg.AIModel = strings.TrimSpace(r.get("HUNTDECK_AI_MODEL", ""))
	cfg.AIKey = Secret(strings.TrimSpace(r.get("HUNTDECK_AI_KEY", "")))
	if (cfg.AIBaseURL == "") != (cfg.AIModel == "") {
		r.errs = append(r.errs, fmt.Errorf("HUNTDECK_AI_BASE_URL and HUNTDECK_AI_MODEL go together: set both, or neither"))
	}
	cfg.PojiaURL = strings.TrimSpace(r.get("HUNTDECK_POJIA_URL", ""))
	if cfg.PojiaURL == "" {
		cfg.PojiaURL = "https://cve.pojia.ai/mcp"
	}
	cfg.PojiaKey = Secret(strings.TrimSpace(r.get("HUNTDECK_POJIA_KEY", "")))
	cfg.ScanURL = strings.TrimSpace(r.get("HUNTDECK_SCAN_URL", ""))
	cfg.ScanKey = Secret(strings.TrimSpace(r.get("HUNTDECK_SCAN_KEY", "")))
	cfg.ScanReadBatch = scanReadBatch(r.get("HUNTDECK_SCAN_READ_BATCH", ""))
	cfg.JevURL = strings.TrimSpace(r.get("HUNTDECK_JEV_URL", ""))
	if cfg.JevURL == "" {
		cfg.JevURL = "https://api.typesafe.ai"
	}
	cfg.JevKey = Secret(strings.TrimSpace(r.get("HUNTDECK_JEV_KEY", "")))
	cfg.JevModel = strings.TrimSpace(r.get("HUNTDECK_JEV_MODEL", ""))
	cfg.JevProxy = strings.TrimSpace(r.get("HUNTDECK_JEV_PROXY", ""))
	cfg.JevProvider = strings.TrimSpace(r.get("HUNTDECK_DECISION_PROVIDER", ""))
	if cfg.JevProvider == "" {
		cfg.JevProvider = "typesafe"
	}
	cfg.JevAccountID = strings.TrimSpace(r.get("HUNTDECK_CF_ACCOUNT_ID", ""))
	cfg.JevWorkspaceID = strings.TrimSpace(r.get("HUNTDECK_BAILIAN_WORKSPACE_ID", ""))
	if cfg.JevModel == "" {
		cfg.JevModel = "jev-latest"
	}
	cfg.Password = Secret(r.get("HUNTDECK_PASSWORD", ""))
	if p := cfg.Password.Reveal(); p != "" && len(p) < 8 {
		r.errs = append(r.errs, errors.New("HUNTDECK_PASSWORD: use at least 8 characters"))
	}

	if cfg.FeedbackEmail = r.get("HUNTDECK_FEEDBACK_EMAIL", ""); cfg.FeedbackEmail != "" {
		if a, err := mail.ParseAddress(cfg.FeedbackEmail); err != nil || a.Name != "" || a.Address != cfg.FeedbackEmail {
			r.errs = append(r.errs, fmt.Errorf("HUNTDECK_FEEDBACK_EMAIL %q: not a plain email address like hello@example.com", cfg.FeedbackEmail))
		}
	}

	if cfg.PublicURL = strings.TrimSuffix(r.get("HUNTDECK_PUBLIC_URL", ""), "/"); cfg.PublicURL != "" {
		if u, err := url.Parse(cfg.PublicURL); err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			r.errs = append(r.errs, fmt.Errorf("HUNTDECK_PUBLIC_URL %q: not an https address like https://demo.example.com", cfg.PublicURL))
		}
	}

	if cfg.XAccount = strings.TrimPrefix(r.get("HUNTDECK_X_ACCOUNT", ""), "@"); cfg.XAccount != "" && !validXAccount(cfg.XAccount) {
		r.errs = append(r.errs, fmt.Errorf("HUNTDECK_X_ACCOUNT %q: not an X handle like huntdeck (letters, digits and _, at most 15)", cfg.XAccount))
	}

	if cfg.RepoURL = strings.TrimSuffix(r.get("HUNTDECK_REPO_URL", ""), "/"); cfg.RepoURL != "" {
		if u, err := url.Parse(cfg.RepoURL); err != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			r.errs = append(r.errs, fmt.Errorf("HUNTDECK_REPO_URL %q: not an https address like https://github.com/mdpsec/huntdeck", cfg.RepoURL))
		}
	}

	if _, _, err := net.SplitHostPort(cfg.Listen); err != nil {
		r.errs = append(r.errs, fmt.Errorf("HUNTDECK_LISTEN %q: %w", cfg.Listen, err))
	}
	if cfg.DataDir == "" {
		r.errs = append(r.errs, errors.New("HUNTDECK_DATA_DIR must not be empty"))
	}
	if cfg.LogFormat != "text" && cfg.LogFormat != "json" {
		r.errs = append(r.errs, fmt.Errorf("HUNTDECK_LOG_FORMAT %q: must be text or json", cfg.LogFormat))
	}
	if err := cfg.LogLevel.UnmarshalText([]byte(r.get("HUNTDECK_LOG_LEVEL", "info"))); err != nil {
		r.errs = append(r.errs, fmt.Errorf("HUNTDECK_LOG_LEVEL: %w", err))
	}
	cfg.Chrome = r.get("HUNTDECK_CHROME", "")
	cfg.ChromeHeadless = r.bool("HUNTDECK_CHROME_HEADLESS", false)
	cfg.ChromeTimezone = r.get("HUNTDECK_CHROME_TIMEZONE", cfg.Timezone)
	cfg.VNC = r.bool("HUNTDECK_VNC", false)
	cfg.NoVNCDir = r.get("HUNTDECK_NOVNC_DIR", "/usr/share/novnc")
	if !ValidTimezone(cfg.ChromeTimezone) {
		r.errs = append(r.errs, fmt.Errorf("HUNTDECK_CHROME_TIMEZONE %q: not a time zone name like America/New_York", cfg.ChromeTimezone))
	}
	if p := r.get("HUNTDECK_PROXY", ""); p != "" {
		u, err := url.Parse(p)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5") || u.Host == "" {
			r.errs = append(r.errs, errors.New("HUNTDECK_PROXY: must be a URL like http://127.0.0.1:8080 or socks5://host:port"))
		} else {
			cfg.Proxy = u
		}
	}

	for _, h := range strings.Split(r.get("HUNTDECK_ALLOWED_HOSTS", ""), ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			cfg.AllowedHosts = append(cfg.AllowedHosts, h)
		}
	}

	cfg.HackerOne = HackerOne{
		Username:      r.get("H1_USERNAME", ""),
		Token:         Secret(r.get("H1_TOKEN", "")),
		Email:         r.get("H1_EMAIL", ""),
		Password:      Secret(r.get("H1_PASSWORD", "")),
		TOTPSecret:    Secret(r.get("H1_TOTP_SECRET", r.get("H1_OTP", ""))),
		FullInterval:  r.duration("H1_FULL_INTERVAL", 6*time.Hour),
		QuickInterval: r.duration("H1_QUICK_INTERVAL", 15*time.Minute),
		RatePerSecond: r.float("H1_RATE", 5, 0.1, 10),
		Concurrency:   r.int("H1_CONCURRENCY", 4, 1, 16),
	}

	cfg.Bugcrowd = Bugcrowd{
		Email:         r.get("BC_EMAIL", ""),
		Password:      Secret(r.get("BC_PASSWORD", "")),
		TOTPSecret:    Secret(r.get("BC_TOTP_SECRET", "")),
		Session:       Secret(r.get("BC_SESSION", "")),
		PublicOnly:    r.bool("BC_PUBLIC_ONLY", false),
		FullInterval:  r.duration("BC_FULL_INTERVAL", 6*time.Hour),
		QuickInterval: r.duration("BC_QUICK_INTERVAL", 30*time.Minute),
		RatePerSecond: r.float("BC_RATE", 1, 0.1, 3),
		Concurrency:   r.int("BC_CONCURRENCY", 2, 1, 4),
	}

	cfg.Intigriti = Intigriti{
		Token:         Secret(r.get("IT_TOKEN", "")),
		Email:         r.get("IT_EMAIL", ""),
		Password:      Secret(r.get("IT_PASSWORD", "")),
		TOTPSecret:    Secret(r.get("IT_TOTP_SECRET", "")),
		FullInterval:  r.duration("IT_FULL_INTERVAL", 6*time.Hour),
		QuickInterval: r.duration("IT_QUICK_INTERVAL", 15*time.Minute),
		RatePerSecond: r.float("IT_RATE", 2, 0.1, 10),
		Concurrency:   r.int("IT_CONCURRENCY", 2, 1, 8),
	}

	cfg.YesWeHack = YesWeHack{
		Email:         r.get("YWH_EMAIL", ""),
		Password:      Secret(r.get("YWH_PASSWORD", "")),
		TOTPSecret:    Secret(r.get("YWH_TOTP_SECRET", "")),
		Token:         Secret(r.get("YWH_TOKEN", "")),
		PublicOnly:    r.bool("YWH_PUBLIC_ONLY", false),
		FullInterval:  r.duration("YWH_FULL_INTERVAL", 6*time.Hour),
		QuickInterval: r.duration("YWH_QUICK_INTERVAL", 30*time.Minute),
		RatePerSecond: r.float("YWH_RATE", 2, 0.1, 5),
		Concurrency:   r.int("YWH_CONCURRENCY", 2, 1, 4),
	}

	cfg.VulBox = VulBox{
		Account:       r.get("VB_ACCOUNT", ""),
		Password:      Secret(r.get("VB_PASSWORD", "")),
		TOTPSecret:    Secret(r.get("VB_TOTP_SECRET", "")),
		Session:       Secret(r.get("VB_SESSION", "")),
		AutoJoin:      r.bool("VB_AUTO_JOIN", true),
		PublicOnly:    r.bool("VB_PUBLIC_ONLY", false),
		FullInterval:  r.duration("VB_FULL_INTERVAL", 6*time.Hour),
		QuickInterval: r.duration("VB_QUICK_INTERVAL", 30*time.Minute),
		Rate:          r.float("VB_RATE", 1, 0.1, 5),
		Concurrency:   r.int("VB_CONCURRENCY", 2, 1, 4),
	}

	cfg.IssueHunt = IssueHunt{
		APIKey:        Secret(r.get("IH_API_KEY", "")),
		PublicOnly:    r.bool("IH_PUBLIC_ONLY", false),
		FullInterval:  r.duration("IH_FULL_INTERVAL", 6*time.Hour),
		QuickInterval: r.duration("IH_QUICK_INTERVAL", 30*time.Minute),
		Rate:          r.float("IH_RATE", 1.5, 0.1, 5),
		Concurrency:   r.int("IH_CONCURRENCY", 2, 1, 4),
	}

	cfg.GObugfree = GObugfree{
		Email:         r.get("GBF_EMAIL", ""),
		Password:      Secret(r.get("GBF_PASSWORD", "")),
		TOTPSecret:    Secret(r.get("GBF_TOTP_SECRET", "")),
		Token:         Secret(r.get("GBF_TOKEN", "")),
		PublicOnly:    r.bool("GBF_PUBLIC_ONLY", false),
		FullInterval:  r.duration("GBF_FULL_INTERVAL", 6*time.Hour),
		QuickInterval: r.duration("GBF_QUICK_INTERVAL", 30*time.Minute),
		Rate:          r.float("GBF_RATE", 1.5, 0.1, 5),
		Concurrency:   r.int("GBF_CONCURRENCY", 2, 1, 4),
	}

	cfg.Inspectiv = Inspectiv{
		Email:         r.get("INS_EMAIL", ""),
		Password:      Secret(r.get("INS_PASSWORD", "")),
		Token:         Secret(r.get("INS_TOKEN", "")),
		FullInterval:  r.duration("INS_FULL_INTERVAL", 6*time.Hour),
		QuickInterval: r.duration("INS_QUICK_INTERVAL", 30*time.Minute),
		Rate:          r.float("INS_RATE", 1.5, 0.1, 5),
		Concurrency:   r.int("INS_CONCURRENCY", 2, 1, 4),
	}

	cfg.HuntMail = HuntMail{URL: Secret(strings.TrimSpace(r.get("HUNTMAIL_URL", ""))), Key: Secret(strings.TrimSpace(r.get("HUNTMAIL_API_KEY", "")))}
	if h := cfg.HuntMail; (h.URL == "") != (h.Key == "") {
		r.errs = append(r.errs, errors.New("HUNTMAIL_URL and HUNTMAIL_API_KEY must be set together"))
	}
	if u := cfg.HuntMail.URL; u != "" {
		if err := huntMailURL(u.Reveal()); err != nil {
			r.errs = append(r.errs, fmt.Errorf("HUNTMAIL_URL: %w", err))
		}
	}
	for _, d := range strings.FieldsFunc(r.get("HUNTMAIL_DOMAINS", ""), func(c rune) bool { return c == ',' || c == ' ' }) {
		d = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d), "@"))
		if !validDomain(d) {
			r.errs = append(r.errs, fmt.Errorf("HUNTMAIL_DOMAINS: %q is not a domain like example.com", d))
			continue
		}
		cfg.HuntMail.Domains = append(cfg.HuntMail.Domains, d)
	}

	list, errs := parseSources(r.get("HUNTDECK_SOURCES", ""))
	cfg.Sources = Sources{List: list, Interval: r.duration("HUNTDECK_SOURCES_INTERVAL", time.Hour)}
	r.errs = append(r.errs, errs...)

	cfg.ReadOnly = r.bool("HUNTDECK_READ_ONLY", false)
	if cfg.ReadOnly {
		r.errs = append(r.errs, readOnlyConflicts(cfg)...)
	}
	cfg.SeedURL = seedURL(r.get("HUNTDECK_SEED_URL", DefaultSeedURL), &r.errs)

	return cfg, errors.Join(r.errs...)
}

type resolver struct {
	src  Source
	errs []error
}

func (r *resolver) duration(name string, def time.Duration) time.Duration {
	v := r.get(name, "")
	if v == "" {
		return def
	}
	if v == "0" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < time.Minute {
		r.errs = append(r.errs, fmt.Errorf("%s %q: must be a duration of at least 1m (e.g. 15m, 6h) or 0 to disable", name, v))
		return def
	}
	return d
}

func (r *resolver) bool(name string, def bool) bool {
	v := r.get(name, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s %q: must be true or false", name, v))
		return def
	}
	return b
}

func (r *resolver) float(name string, def, lo, hi float64) float64 {
	v := r.get(name, "")
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < lo || f > hi {
		r.errs = append(r.errs, fmt.Errorf("%s %q: must be a number from %g to %g", name, v, lo, hi))
		return def
	}
	return f
}

func (r *resolver) int(name string, def, lo, hi int) int {
	v := r.get(name, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		r.errs = append(r.errs, fmt.Errorf("%s %q: must be a whole number from %d to %d", name, v, lo, hi))
		return def
	}
	return n
}

// get returns NAME, or the trimmed contents of the file named by NAME_FILE,
// or def when neither is set.
func (r *resolver) get(name, def string) string {
	val, hasVal := r.src.LookupEnv(name)
	path, hasFile := r.src.LookupEnv(name + "_FILE")
	switch {
	case hasVal && hasFile:
		r.errs = append(r.errs, fmt.Errorf("%s and %s_FILE are both set; use one", name, name))
		return def
	case hasFile:
		b, err := r.src.ReadFile(path)
		if err != nil {
			// Don't include the error text: it can echo file contents on some platforms.
			r.errs = append(r.errs, fmt.Errorf("%s_FILE: cannot read %q", name, path))
			return def
		}
		return strings.TrimSpace(string(b))
	case hasVal:
		return val
	default:
		return def
	}
}

// ValidTimezone reports whether name is an IANA time zone name (or UTC).
// "Local" and "" aren't: they'd mean whatever the container happens to use.
func ValidTimezone(name string) bool {
	if name == "" || name == "Local" {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

// readOnlyConflicts are the settings a read-only install refuses: anything
// that would make it poll, sign in or reach a platform as an account.
func readOnlyConflicts(cfg Config) []error {
	var out []error
	for _, c := range []struct {
		on   bool
		what string
	}{
		{cfg.HackerOne.Enabled() || cfg.HackerOne.Email != "", "HackerOne credentials (H1_*)"},
		{cfg.Bugcrowd.Enabled(), "Bugcrowd credentials (BC_*)"},
		{cfg.Intigriti.Enabled() || cfg.Intigriti.Email != "", "Intigriti credentials (IT_*)"},
		{cfg.YesWeHack.Enabled(), "YesWeHack credentials (YWH_*)"},
		{cfg.VulBox.Enabled(), "VulBox credentials (VB_*)"},
		{cfg.IssueHunt.Enabled(), "IssueHunt credentials (IH_*)"},
		{cfg.GObugfree.Enabled(), "GObugfree credentials (GBF_*)"},
		{cfg.Inspectiv.Enabled(), "Inspectiv credentials (INS_*)"},
		{cfg.VNC, "HUNTDECK_VNC"},
		{cfg.Proxy != nil, "HUNTDECK_PROXY"},
		{len(cfg.Sources.List) > 0, "HUNTDECK_SOURCES"},
		{cfg.HuntMail.URL != "" || cfg.HuntMail.Key != "", "HUNTMAIL_URL and HUNTMAIL_API_KEY"},
	} {
		if c.on {
			out = append(out, fmt.Errorf("HUNTDECK_READ_ONLY never polls or signs in: unset %s", c.what))
		}
	}
	return out
}

// huntMailURL checks a HuntMail address without saying what it is: it
// holds a secret. https (http only for this machine), with a base path.
func huntMailURL(raw string) error {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	switch {
	case err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "":
		return errors.New("should look like https://<worker>.<subdomain>.workers.dev/<base path>, as HuntMail's setup printed it")
	case u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1")):
		return errors.New("must start with https://")
	case strings.Trim(u.Path, "/") == "":
		return errors.New("needs its base path, the part after the host that HuntMail's setup printed")
	}
	return nil
}

// validDomain reports whether d looks like a domain name: labels of
// letters, digits and dashes, at least two of them.
func validDomain(d string) bool {
	labels := strings.Split(d, ".")
	if len(d) > 253 || len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for _, c := range l {
			if c != '-' && (c < '0' || c > '9') && (c < 'a' || c > 'z') {
				return false
			}
		}
	}
	return true
}

// validHeaderName reports whether name is an HTTP header name: letters,
// digits and dashes.
func validHeaderName(name string) bool {
	for _, c := range name {
		ok := c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !ok {
			return false
		}
	}
	return name != "" && len(name) <= 64
}

// validXAccount reports whether s is an X handle: 1 to 15 letters, digits
// and underscores.
func validXAccount(s string) bool {
	if len(s) == 0 || len(s) > 15 {
		return false
	}
	for _, c := range s {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
		if !ok {
			return false
		}
	}
	return true
}

// seedURL reads HUNTDECK_SEED_URL: "off" (or "none", "false", "0") for no
// starter seed, else an https URL.
func seedURL(v string, errs *[]error) string {
	v = strings.TrimSpace(v)
	switch strings.ToLower(v) {
	case "", "off", "none", "false", "0":
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		*errs = append(*errs, errors.New(`HUNTDECK_SEED_URL must be an https URL, or "off"`))
		return ""
	}
	return v
}

// Check runs the rules that need the whole picture, not one source.
//
// They cannot live where the values are read: a TOTP secret is often in the
// environment while the email and password it belongs to are saved in Settings,
// and a check that only sees the environment refuses a configuration that is
// complete. This was a real outage - a secret in .env next to credentials in
// Settings stopped the container from starting, because the reader loaded the
// environment, found no email, and failed before Settings was ever opened.
//
// So the reader parses and checks each value on its own; this runs afterwards,
// on the merged configuration, wherever both sources have been joined
// (internal/overrides). Callers that only read the environment do not call it.
func Check(cfg Config) error {
	var errs []error
	if (cfg.HackerOne.Username == "") != (cfg.HackerOne.Token == "") {
		errs = append(errs, errors.New("H1_USERNAME and H1_TOKEN must be set together"))
	}
	if h := cfg.HackerOne; (h.Email == "") != (h.Password == "") {
		errs = append(errs, errors.New("H1_EMAIL and H1_PASSWORD must be set together"))
	}
	if b := cfg.Bugcrowd; (b.Email == "") != (b.Password == "") {
		errs = append(errs, errors.New("BC_EMAIL and BC_PASSWORD must be set together"))
	}
	if b := cfg.Bugcrowd; b.TOTPSecret != "" && b.Email == "" {
		errs = append(errs, errors.New("BC_TOTP_SECRET needs BC_EMAIL and BC_PASSWORD"))
	}
	if i := cfg.Intigriti; (i.Email == "") != (i.Password == "") {
		errs = append(errs, errors.New("IT_EMAIL and IT_PASSWORD must be set together"))
	}
	if i := cfg.Intigriti; i.Email != "" && i.Token == "" {
		errs = append(errs, errors.New("IT_EMAIL needs IT_TOKEN: the web login only adds stats to the API's programs"))
	}
	if i := cfg.Intigriti; i.TOTPSecret != "" && i.Email == "" {
		errs = append(errs, errors.New("IT_TOTP_SECRET needs IT_EMAIL and IT_PASSWORD"))
	}
	if y := cfg.YesWeHack; (y.Email == "") != (y.Password == "") {
		errs = append(errs, errors.New("YWH_EMAIL and YWH_PASSWORD must be set together"))
	}
	if y := cfg.YesWeHack; y.TOTPSecret != "" && y.Email == "" {
		errs = append(errs, errors.New("YWH_TOTP_SECRET needs YWH_EMAIL and YWH_PASSWORD"))
	}
	if v := cfg.VulBox; (v.Account == "") != (v.Password == "") {
		errs = append(errs, errors.New("VB_ACCOUNT and VB_PASSWORD must be set together"))
	}
	if v := cfg.VulBox; v.TOTPSecret != "" && v.Account == "" {
		errs = append(errs, errors.New("VB_TOTP_SECRET needs VB_ACCOUNT and VB_PASSWORD"))
	}
	if g := cfg.GObugfree; (g.Email == "") != (g.Password == "") {
		errs = append(errs, errors.New("GBF_EMAIL and GBF_PASSWORD must be set together"))
	}
	if g := cfg.GObugfree; g.TOTPSecret != "" && g.Email == "" {
		errs = append(errs, errors.New("GBF_TOTP_SECRET needs GBF_EMAIL and GBF_PASSWORD"))
	}
	if i := cfg.Inspectiv; (i.Email == "") != (i.Password == "") {
		errs = append(errs, errors.New("INS_EMAIL and INS_PASSWORD must be set together"))
	}
	return errors.Join(errs...)
}

// scanReadBatch parses the read-back batch size: eight unless a sane number is
// given, because a pass that asks for a thousand is refused on the first call and
// reports nothing read.
func scanReadBatch(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 {
		return 8
	}
	if n > 100 {
		return 100
	}
	return n
}
