// Package inspectiv implements platform.Platform and optional interfaces for
// Inspectiv (https://app.inspectiv.com / https://api.bounty.inspectiv.com/api).
//
// Inspectiv authenticates researcher API calls with a static Django REST
// Framework (DRF) token in the Authorization header ("Authorization: Token <hex>").
// HuntDeck accepts either a pasted INS_TOKEN or email/password via the shared
// login browser (browserlogin.go), capturing localStorage["inspectiv"] and
// persisting it as a synthetic cookie (inspectiv_token) in sessionjar.Jar.
//
// HuntDeck NEVER calls /accounts/logout/ during polling (which revokes the token
// server-side) and NEVER fetches submission write-ups or thread messages.
package inspectiv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/sessionjar"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// Name is the platform identifier stored in the database.
const Name = "inspectiv"

// WebHome is the signed-in dashboard opened by the shared login browser.
const WebHome = "https://app.inspectiv.com/#/programs"

const (
	defaultBaseURL = "https://api.bounty.inspectiv.com/api"
	defaultWebURL  = "https://app.inspectiv.com"

	defaultRate        = 1.5
	defaultConcurrency = 2
	tokenCookie        = "inspectiv_token"
	currencyUSD        = "USD"
	maxPages           = 200
)

// ErrSessionExpired means Inspectiv rejected the token and HuntDeck cannot
// renew it automatically.
var ErrSessionExpired = errors.New("inspectiv: token rejected or session expired; set a fresh INS_TOKEN or sign in via Settings > Browser")

// Compile-time interface assertions.
var (
	_ platform.Platform           = (*Client)(nil)
	_ platform.ScopeDetailFetcher = (*Client)(nil)
	_ platform.CredentialReporter = (*Client)(nil)
	_ platform.AccountNamer       = (*Client)(nil)
	_ platform.KeepAliver         = (*Client)(nil)
	_ platform.ReportFetcher      = (*Client)(nil)
	_ platform.StatsFetcher       = (*Client)(nil)
)

// Options configures an Inspectiv Client.
type Options struct {
	BaseURL     string  // default "https://api.bounty.inspectiv.com/api"
	WebURL      string  // default "https://app.inspectiv.com"
	Email       string  // from INS_EMAIL
	Password    string  // from INS_PASSWORD
	Token       string  // raw 40-hex DRF token or "Token <hex>" (from INS_TOKEN)
	Rate        float64 // default 1.5
	Concurrency int     // default 2
	ProxyURL    string
	Jar         http.CookieJar // *sessionjar.Jar for persistence + Checkpoint/Rollback
	LoginState  string         // path to sessions/inspectiv-login.json
	WebHost     *weblogin.Host
	Log         *slog.Logger
}

// Client talks to Inspectiv. It is safe for concurrent use.
type Client struct {
	http *httpclient.Client
	jar  http.CookieJar

	base       *url.URL // https://api.bounty.inspectiv.com/api (or httptest server)
	baseOrigin *url.URL // scheme://host of c.base
	web        *url.URL // https://app.inspectiv.com
	defAPI     *url.URL // canonical https://api.bounty.inspectiv.com
	defWeb     *url.URL // canonical https://app.inspectiv.com

	email       string
	password    string
	hasToken    bool
	concurrency int
	log         *slog.Logger

	host    *weblogin.Host
	account *weblogin.Account
	logins  *weblogin.History

	// Test hook.
	browserLoginFn func(context.Context) error

	authMu sync.Mutex
	cred   platform.CredentialState

	stateMu  sync.RWMutex
	token    string
	username string

	metaMu sync.Mutex
	meta   map[string]programJSON // UUID -> cached program detail from /programs/all/
}

// New builds an Inspectiv Client.
func New(opts Options) (*Client, error) {
	email := strings.TrimSpace(opts.Email)
	if (email == "") != (opts.Password == "") {
		return nil, errors.New("inspectiv: email and password must be set together")
	}
	rate := opts.Rate
	if rate <= 0 {
		rate = defaultRate
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	c := &Client{
		email:       email,
		password:    opts.Password,
		concurrency: concurrency,
		log:         log,
		meta:        map[string]programJSON{},
	}
	c.browserLoginFn = c.browserLogin

	var err error
	for _, item := range []struct {
		dst  **url.URL
		val  string
		def  string
		name string
	}{
		{&c.base, opts.BaseURL, defaultBaseURL, "base"},
		{&c.web, opts.WebURL, defaultWebURL, "web"},
		{&c.defAPI, "", "https://api.bounty.inspectiv.com", "default API"},
		{&c.defWeb, "", defaultWebURL, "default web"},
	} {
		raw := item.val
		if raw == "" {
			raw = item.def
		}
		if *item.dst, err = url.Parse(strings.TrimSuffix(raw, "/")); err != nil {
			return nil, fmt.Errorf("inspectiv: %s URL: %w", item.name, err)
		}
	}
	c.baseOrigin = &url.URL{Scheme: c.base.Scheme, Host: c.base.Host, Path: "/"}

	var proxyURL *url.URL
	if strings.TrimSpace(opts.ProxyURL) != "" {
		if proxyURL, err = url.Parse(strings.TrimSpace(opts.ProxyURL)); err != nil {
			return nil, fmt.Errorf("inspectiv: proxy URL: %w", err)
		}
	}

	if opts.Jar != nil {
		c.jar = opts.Jar
	} else if c.jar, err = cookiejar.New(nil); err != nil {
		return nil, err
	}

	if c.email != "" {
		c.host = opts.WebHost
		c.logins = weblogin.NewHistory(opts.LoginState)
		c.account = &weblogin.Account{
			Who:      who,
			Email:    c.email,
			Password: opts.Password,
		}
	}

	if strings.TrimSpace(opts.Token) != "" {
		tok := normalizeToken(opts.Token)
		if tok == "" {
			return nil, errors.New("inspectiv: INS_TOKEN is empty")
		}
		c.hasToken = true
		c.seedToken(tok)
	}

	c.http = httpclient.New(httpclient.Options{
		RatePerSecond: rate,
		Burst:         max(2, concurrency),
		Proxy:         proxyURL,
		Jar:           c.jar,
		RedirectHosts: []string{c.base.Host, c.web.Host},
	})
	return c, nil
}

// Name implements platform.Platform.
func (c *Client) Name() string { return Name }

// Account implements platform.AccountNamer.
func (c *Client) Account() string {
	c.stateMu.RLock()
	user := c.username
	c.stateMu.RUnlock()
	if user != "" {
		return strings.ToLower(strings.TrimSpace(user))
	}
	if c.email != "" {
		return strings.ToLower(strings.TrimSpace(c.email))
	}
	return ""
}

// Stats implements platform.Platform.
func (c *Client) Stats() (requests, rateLimited int64) {
	return c.http.Stats()
}

// Credentials implements platform.CredentialReporter.
func (c *Client) Credentials() []platform.Credential {
	if c.email != "" || c.host != nil {
		return []platform.Credential{c.cred.Report("Website login")}
	}
	if c.hasToken || c.currentToken() != "" {
		return []platform.Credential{c.cred.Report("API token")}
	}
	return nil
}

// KeepAlive implements platform.KeepAliver.
func (c *Client) KeepAlive(ctx context.Context) error {
	if !c.authenticated() {
		return nil
	}
	return c.ensureSession(ctx)
}

func (c *Client) authenticated() bool {
	return c.hasToken || c.email != "" || c.currentToken() != ""
}

func (c *Client) canBrowserLogin() bool {
	return c.email != "" && c.host != nil
}

func (c *Client) ensureSession(ctx context.Context) error {
	if !c.authenticated() {
		return ErrSessionExpired
	}
	c.authMu.Lock()
	defer c.authMu.Unlock()

	if c.currentToken() != "" {
		ok, err := c.verifySession(ctx)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}

	if !c.canBrowserLogin() {
		if c.email != "" {
			c.cred.Refused("Inspectiv website login requires the login browser or INS_TOKEN", false)
		} else {
			c.cred.Refused("Inspectiv rejected the API token: set a fresh INS_TOKEN", true)
		}
		return ErrSessionExpired
	}

	if err := c.browserSignIn(ctx); err != nil {
		return err
	}
	ok, err := c.verifySession(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s: login finished but Inspectiv rejected the captured token", who)
	}
	return nil
}

func (c *Client) renewSession(ctx context.Context) error {
	c.authMu.Lock()
	defer c.authMu.Unlock()

	if !c.canBrowserLogin() {
		if c.email != "" {
			c.cred.Refused("Inspectiv website login requires the login browser or INS_TOKEN", false)
		} else {
			c.cred.Refused("Inspectiv rejected the API token (401): set a fresh INS_TOKEN", true)
		}
		return ErrSessionExpired
	}

	if err := c.browserSignIn(ctx); err != nil {
		return err
	}
	ok, err := c.verifySession(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return ErrSessionExpired
	}
	return nil
}

type accountUserResp struct {
	Token          string `json:"token"`
	UserType       string `json:"user_type"`
	Active         bool   `json:"active"`
	EmailConfirmed bool   `json:"email_confirmed"`
}

type accountProfileResp struct {
	UUID     string `json:"uuid"`
	UserName string `json:"user_name"`
	UserType string `json:"user_type"`
	Email    string `json:"email"`
}

// verifySession validates the current DRF token via GET /accounts/user and
// populates the AccountNamer username from GET /accounts/profile/.
func (c *Client) verifySession(ctx context.Context) (bool, error) {
	var user accountUserResp
	if err := c.getJSONNoRenew(ctx, "/accounts/user", &user); err != nil {
		if errors.Is(err, ErrSessionExpired) {
			return false, nil
		}
		return false, err
	}
	if user.Token != "" && !user.Active {
		c.cred.Refused("Inspectiv account is inactive", true)
		return false, ErrSessionExpired
	}

	if c.Account() == "" || c.currentUsername() == "" {
		var prof accountProfileResp
		if err := c.getJSONNoRenew(ctx, "/accounts/profile/", &prof); err != nil {
			if errors.Is(err, ErrSessionExpired) {
				return false, nil
			}
			var ae *apiError
			if !errors.As(err, &ae) || ae.StatusCode != http.StatusNotFound {
				return false, err
			}
		} else if name := strings.TrimSpace(prof.UserName); name != "" {
			c.setUsername(name)
		} else if em := strings.TrimSpace(prof.Email); em != "" {
			c.setUsername(em)
		}
	}

	c.cred.Accepted()
	return true, nil
}

// normalizeToken accepts either a raw 40-hex DRF token or "Token <hex>".
func normalizeToken(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) >= len("token ") && strings.EqualFold(s[:len("token ")], "token ") {
		s = strings.TrimSpace(s[len("token "):])
	}
	return s
}

func (c *Client) targetURLs() []*url.URL {
	candidates := []*url.URL{c.baseOrigin, c.web, c.defAPI, c.defWeb}
	seen := make(map[string]bool, len(candidates))
	out := make([]*url.URL, 0, len(candidates))
	for _, u := range candidates {
		if u == nil {
			continue
		}
		root := &url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/"}
		key := root.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, root)
	}
	return out
}

func (c *Client) setToken(tok string) {
	c.stateMu.Lock()
	c.token = strings.TrimSpace(tok)
	c.stateMu.Unlock()
}

func (c *Client) setUsername(name string) {
	c.stateMu.Lock()
	c.username = strings.TrimSpace(name)
	c.stateMu.Unlock()
}

func (c *Client) currentUsername() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.username
}

// seedToken saves tok in memory and persists it as the synthetic inspectiv_token
// cookie in c.jar on api.bounty.inspectiv.com and app.inspectiv.com (plus any
// custom BaseURL/WebURL hosts).
func (c *Client) seedToken(tok string) {
	tok = normalizeToken(tok)
	c.setToken(tok)
	if c.jar == nil {
		return
	}
	clearCk := []*http.Cookie{{Name: tokenCookie, Path: "/", MaxAge: -1}} //nolint:gosec // expire stale synthetic token cookie
	urls := c.targetURLs()
	for _, u := range urls {
		c.jar.SetCookies(u, clearCk)
	}
	if tok == "" {
		return
	}
	for _, u := range urls {
		c.jar.SetCookies(u, []*http.Cookie{{
			Name:  tokenCookie,
			Value: tok,
			Path:  "/",
		}})
	}
}

// currentToken returns the active DRF token from memory, or restores it from
// the synthetic inspectiv_token cookie in c.jar.
func (c *Client) currentToken() string {
	c.stateMu.RLock()
	tok := c.token
	c.stateMu.RUnlock()
	if tok != "" {
		return tok
	}
	if c.jar == nil {
		return ""
	}
	for _, u := range c.targetURLs() {
		for _, ck := range c.jar.Cookies(u) {
			if ck.Name == tokenCookie {
				if v := normalizeToken(ck.Value); v != "" {
					return v
				}
			}
		}
	}
	return ""
}

func (c *Client) setMeta(uuid string, p programJSON) {
	c.metaMu.Lock()
	c.meta[uuid] = p
	c.metaMu.Unlock()
}

func (c *Client) getMeta(uuid string) (programJSON, bool) {
	c.metaMu.Lock()
	p, ok := c.meta[uuid]
	c.metaMu.Unlock()
	return p, ok
}

// BrowserSession returns a weblogin.StorageSession for the shared Chrome tab,
// restoring saved cookies and localStorage["inspectiv"] when opened via VNC.
func (c *Client) BrowserSession() weblogin.Session { return webSession{c} }

type webSession struct{ c *Client }

func (s webSession) Hosts() []string {
	if sj, ok := s.c.jar.(*sessionjar.Jar); ok {
		return sj.Hosts()
	}
	return nil
}

func (s webSession) Saved(host string) []*http.Cookie {
	if sj, ok := s.c.jar.(*sessionjar.Jar); ok {
		return sj.Saved(host)
	}
	return nil
}

// LocalStorage implements weblogin.StorageSession.
func (s webSession) LocalStorage() (string, map[string]string) {
	tok := s.c.currentToken()
	if tok == "" || s.c.web == nil {
		return "", nil
	}
	origin := (&url.URL{Scheme: s.c.web.Scheme, Host: s.c.web.Host}).String()
	return origin, map[string]string{localStorageKey: tok}
}
