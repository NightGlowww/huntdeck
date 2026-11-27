// Package vulbox implements platform.Platform and optional interfaces for
// VulBox (漏洞盒子).
//
// VulBox serves a unified JSON REST API at https://vapi.vulbox.com (also
// reverse-proxied under https://user.vulbox.com/api/*). Almost every endpoint
// returns HTTP 200 with an application-level envelope {code, msg, data}, so
// every response is checked at the envelope layer as well as the HTTP layer.
//
// Three operating modes:
//   - Public-only (no credentials or PublicOnly=true): reads public project
//     listings and the Enterprise SRC directory without signing in.
//   - Pasted session (Session): accepts a raw vulbox_token JWT, "Bearer <jwt>",
//     or a Cookie header, sending both Authorization: Bearer and cookies.
//   - Website login (Account + Password + optional TOTPSecret via WebHost):
//     signs in with Chrome in the shared login browser (browserlogin.go) and
//     persists cookies in Jar (*sessionjar.Jar).
//
// When authenticated, ListPrograms checks the session before and after paging
// so an expired session never looks like removed private programs.
package vulbox

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
	"sync/atomic"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// Name is the platform identifier stored in the database.
const Name = "vulbox"

// WebHome is the signed-in dashboard opened by the shared login browser.
const WebHome = "https://user.vulbox.com/"

const (
	defaultBaseURL = "https://vapi.vulbox.com"
	defaultWebURL  = "https://www.vulbox.com"
	defaultUserURL = "https://user.vulbox.com"
	defaultRootURL = "https://vulbox.com"

	tokenCookie     = "vulbox_token"
	uuidCookie      = "uuid"
	currencyCNY     = "CNY"
	maxPages        = 200
	vulboxUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

var (
	// ErrSessionExpired means VulBox no longer accepts the session and
	// HuntDeck cannot renew it automatically.
	ErrSessionExpired = errors.New("vulbox: session expired or invalid; paste a fresh VB_SESSION or sign in via Settings > Browser")
	// ErrMFARequired means the account requires TOTP verification (code 403004).
	ErrMFARequired = errors.New("vulbox: account requires 2FA verification (code 403004); set VB_TOTP_SECRET or complete sign-in in Settings > Browser")
	// ErrInitPassword means VulBox requires resetting the initial password (code 403005).
	ErrInitPassword = errors.New("vulbox: account must reset its initial password on vulbox.com (code 403005)")
)

// Compile-time interface assertions.
var (
	_ platform.Platform           = (*Client)(nil)
	_ platform.ScopeDetailFetcher = (*Client)(nil)
	_ platform.CredentialReporter = (*Client)(nil)
	_ platform.KeepAliver         = (*Client)(nil)
	_ platform.AccountNamer       = (*Client)(nil)
	_ platform.InviteLister       = (*Client)(nil)
	_ platform.InviteAccepter     = (*Client)(nil)
	_ platform.ReportFetcher      = (*Client)(nil)
)

// Options configures a VulBox Client.
type Options struct {
	BaseURL     string  // default "https://vapi.vulbox.com"
	WebURL      string  // default "https://www.vulbox.com"
	Account     string  // username / phone / email (from VB_ACCOUNT)
	Password    string  // from VB_PASSWORD
	TOTPSecret  string  // optional base32 TOTP (from VB_TOTP_SECRET)
	Session     string  // optional vulbox_token / Bearer / Cookie header (from VB_SESSION)
	AutoJoin    bool    // from VB_AUTO_JOIN (default true in config)
	PublicOnly  bool    // from VB_PUBLIC_ONLY
	Rate        float64 // default 1
	Concurrency int     // default 2
	ProxyURL    string
	Jar         http.CookieJar // *sessionjar.Jar for persistence + Checkpoint/Rollback
	LoginState  string         // path to sessions/vulbox-login.json
	WebHost     *weblogin.Host
	Log         *slog.Logger
}

// Client talks to VulBox. It is safe for concurrent use.
type Client struct {
	http   *httpclient.Client // retries network/5xx/429 for safe GETs
	action *httpclient.Client // MaxAttempts: 1 for state-changing POSTs
	jar    http.CookieJar

	base *url.URL // vapi.vulbox.com (or httptest server)
	web  *url.URL // www.vulbox.com
	user *url.URL // user.vulbox.com
	root *url.URL // vulbox.com (for .vulbox.com domain cookies)

	accountName string
	password    string
	totpSecret  string
	autoJoin    bool
	publicOnly  bool
	hasSession  bool
	clientUUID  string
	log         *slog.Logger

	host    *weblogin.Host
	account *weblogin.Account
	logins  *weblogin.History

	// Test hooks.
	now            func() time.Time
	sleep          func(context.Context, time.Duration) error
	browserLoginFn func(context.Context) error

	authMu sync.Mutex
	cred   platform.CredentialState

	stateMu    sync.RWMutex
	apiBaseURL *url.URL // active API base (c.base, or user.vulbox.com/api on live fallback)
	token      string   // explicit active vulbox_token synced from login/Take or Session
	userAgent  string   // synced from Chrome navigator.userAgent when available

	appRateLimited atomic.Int64

	metaMu sync.Mutex
	meta   map[string]projectMeta // handle -> metadata learned during listing/invites
}

// projectMeta remembers listing/invite fields needed by FetchScopeDetail and AcceptInvite.
type projectMeta struct {
	ID            int64
	SRCURL        string
	InviteCode    string
	IsInvited     bool
	IsJoined      bool
	TakeMargin    bool
	IsMarginWhite bool
	IsSRC         bool
	ProjectType   int
	Title         string
}

// New builds a VulBox Client.
func New(opts Options) (*Client, error) {
	if !opts.PublicOnly && (opts.Account == "") != (opts.Password == "") {
		return nil, errors.New("vulbox: account and password must be set together")
	}
	rate := opts.Rate
	if rate <= 0 {
		rate = 1
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	c := &Client{
		accountName: strings.TrimSpace(opts.Account),
		password:    opts.Password,
		totpSecret:  opts.TOTPSecret,
		autoJoin:    opts.AutoJoin,
		publicOnly:  opts.PublicOnly,
		clientUUID:  newClientUUID(),
		userAgent:   vulboxUserAgent,
		log:         log,
		now:         time.Now,
		sleep:       sleepCtx,
		meta:        map[string]projectMeta{},
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
		{&c.user, "", defaultUserURL, "user"},
		{&c.root, "", defaultRootURL, "root"},
	} {
		raw := item.val
		if raw == "" {
			raw = item.def
		}
		if *item.dst, err = url.Parse(strings.TrimSuffix(raw, "/")); err != nil {
			return nil, fmt.Errorf("vulbox: %s URL: %w", item.name, err)
		}
	}
	c.apiBaseURL = c.base

	var proxyURL *url.URL
	if strings.TrimSpace(opts.ProxyURL) != "" {
		if proxyURL, err = url.Parse(strings.TrimSpace(opts.ProxyURL)); err != nil {
			return nil, fmt.Errorf("vulbox: proxy URL: %w", err)
		}
	}

	if opts.Jar != nil {
		c.jar = opts.Jar
	} else if c.jar, err = cookiejar.New(nil); err != nil {
		return nil, err
	}

	if !opts.PublicOnly && opts.Account != "" {
		c.host = opts.WebHost
		c.logins = weblogin.NewHistory(opts.LoginState)
		c.logins.Now = c.now
		c.account = &weblogin.Account{
			Who:        who,
			Email:      c.accountName,
			Password:   opts.Password,
			TOTPSecret: opts.TOTPSecret,
			TOTPEnv:    "VB_TOTP_SECRET",
			Now:        c.now,
		}
	}

	if !opts.PublicOnly && strings.TrimSpace(opts.Session) != "" {
		cookies := parseSession(opts.Session)
		if len(cookies) == 0 {
			return nil, errors.New("vulbox: VB_SESSION is not a valid token or cookie value")
		}
		c.seedCookies(cookies)
		c.hasSession = true
		c.updateTokenExpiry()
	} else if !opts.PublicOnly && c.currentToken() != "" {
		// Pre-existing session restored from persistent sessionjar.
		c.updateTokenExpiry()
	}

	ho := httpclient.Options{
		RatePerSecond: rate,
		Burst:         2,
		Proxy:         proxyURL,
		Jar:           c.jar,
		UserAgent:     vulboxUserAgent,
		RedirectHosts: []string{c.base.Host, c.web.Host, c.user.Host},
	}
	c.http = httpclient.New(ho)

	ao := ho
	ao.MaxAttempts = 1
	c.action = httpclient.New(ao)
	return c, nil
}

// Name implements platform.Platform.
func (c *Client) Name() string { return Name }

// Account implements platform.AccountNamer.
func (c *Client) Account() string {
	if c.publicOnly {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(c.accountName))
}

// Stats implements platform.Platform.
func (c *Client) Stats() (requests, rateLimited int64) {
	r1, l1 := c.http.Stats()
	r2, l2 := c.action.Stats()
	return r1 + r2, l1 + l2 + c.appRateLimited.Load()
}

// authenticated reports whether the client is configured with credentials.
func (c *Client) authenticated() bool {
	if c.publicOnly {
		return false
	}
	return c.hasSession || c.accountName != ""
}

// canBrowserLogin reports whether Chrome-based website login is configured.
func (c *Client) canBrowserLogin() bool {
	return !c.publicOnly && c.accountName != "" && c.host != nil
}

// Credentials implements platform.CredentialReporter.
// The name "Website login" is required for Settings > Browser VNC links.
func (c *Client) Credentials() []platform.Credential {
	if c.publicOnly {
		return nil
	}
	switch {
	case c.accountName != "" && c.host != nil:
		return []platform.Credential{c.cred.Report("Website login")}
	case c.accountName != "":
		return []platform.Credential{c.cred.Report("Website login")}
	case c.hasSession:
		return []platform.Credential{c.cred.Report("Session cookie")}
	default:
		return nil
	}
}

// KeepAlive implements platform.KeepAliver.
func (c *Client) KeepAlive(ctx context.Context) error {
	if !c.authenticated() {
		return nil
	}
	if c.canBrowserLogin() {
		return c.ensureSession(ctx)
	}
	ok, err := c.sessionValid(ctx)
	if err != nil {
		return err
	}
	if !ok {
		c.cred.Refused("VulBox no longer accepts the session: paste a fresh VB_SESSION or sign in again", true)
		return ErrSessionExpired
	}
	return nil
}

// ensureSession verifies that a live VulBox session is held, refreshing via
// the shared login browser when configured.
func (c *Client) ensureSession(ctx context.Context) error {
	if !c.authenticated() {
		return platform.ErrNoWebLogin
	}
	c.authMu.Lock()
	defer c.authMu.Unlock()

	if c.currentToken() != "" {
		ok, err := c.sessionValid(ctx)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	if !c.canBrowserLogin() {
		if c.accountName != "" {
			c.cred.Refused("VulBox website login requires the login browser or VB_SESSION", false)
		} else {
			c.cred.Refused("VulBox no longer accepts the session: paste a fresh VB_SESSION and restart", true)
		}
		return ErrSessionExpired
	}
	if err := c.browserSignIn(ctx); err != nil {
		return err
	}
	ok, reason, err := c.sessionCheck(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("vulbox: login finished but VulBox did not accept the session (%s)", reason)
	}
	return nil
}

// renewSession forces a browser re-login after an API call received 401/402/401004.
func (c *Client) renewSession(ctx context.Context) error {
	c.authMu.Lock()
	defer c.authMu.Unlock()

	if !c.canBrowserLogin() {
		c.cred.Refused("VulBox session expired or was signed out elsewhere (401/401004): paste a fresh VB_SESSION", true)
		return ErrSessionExpired
	}
	if err := c.browserSignIn(ctx); err != nil {
		return err
	}
	ok, _, err := c.sessionCheck(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return ErrSessionExpired
	}
	return nil
}

func (c *Client) sessionValid(ctx context.Context) (bool, error) {
	ok, _, err := c.sessionCheck(ctx)
	return ok, err
}

// sessionCheck calls GET /web/account/basic-info (and /hacker/user/account/info
// plus https://user.vulbox.com/api/web/account/basic-info as fallbacks when
// holding a non-empty token) and logs structured non-secret diagnostics.
func (c *Client) sessionCheck(ctx context.Context) (bool, string, error) {
	tok := c.currentToken()
	tokLen := len(tok)
	iat, exp := parseJWTMeta(tok)
	jarToks := c.jarTokenCount()
	apiHost := c.apiBase().Host
	hasUUID := c.currentUUID() != ""

	type basicInfoResp struct {
		Logged flexBool `json:"logged"`
		User   struct {
			Enable   flexBool `json:"enable"`
			Username string   `json:"username"`
		} `json:"user"`
	}
	var info basicInfoResp
	err := c.getJSONNoRenew(ctx, "/web/account/basic-info", nil, &info)
	if err != nil {
		if errors.Is(err, ErrSessionExpired) {
			c.log.Warn("vulbox: session check rejected",
				"reason", err.Error(),
				"jwt_len", tokLen,
				"jwt_iat", iat,
				"jwt_exp", exp,
				"jar_toks", jarToks,
				"api_host", apiHost,
				"has_uuid", hasUUID,
			)
			return false, err.Error(), nil
		}
		c.log.Warn("vulbox: session check error",
			"err", err,
			"jwt_len", tokLen,
			"jwt_iat", iat,
			"jwt_exp", exp,
			"jar_toks", jarToks,
			"api_host", apiHost,
			"has_uuid", hasUUID,
		)
		return false, err.Error(), err
	}
	if info.Logged.Bool() {
		if c.authenticated() {
			c.cred.Accepted()
			c.updateTokenExpiry()
		}
		c.log.Info("vulbox: session accepted",
			"endpoint", "/web/account/basic-info",
			"api_host", apiHost,
			"jwt_len", tokLen,
			"jwt_iat", iat,
			"jwt_exp", exp,
			"jar_toks", jarToks,
		)
		return true, "", nil
	}

	// Fallback: if we hold a non-empty vulbox_token, probe /hacker/user/account/info
	// in case basic-info lagged or reacted differently, accepting ONLY if a real
	// user identity (uid or username) is present.
	if tokLen > 0 {
		var hinfo struct {
			UID      flexInt64 `json:"uid"`
			Username string    `json:"username"`
			User     struct {
				UID      flexInt64 `json:"uid"`
				Username string    `json:"username"`
			} `json:"user"`
		}
		herr := c.getJSONNoRenew(ctx, "/hacker/user/account/info", nil, &hinfo)
		if herr == nil && (hinfo.UID.Int64() > 0 || hinfo.Username != "" || hinfo.User.UID.Int64() > 0 || hinfo.User.Username != "") {
			if c.authenticated() {
				c.cred.Accepted()
				c.updateTokenExpiry()
			}
			c.log.Info("vulbox: session accepted",
				"endpoint", "/hacker/user/account/info",
				"api_host", apiHost,
				"jwt_len", tokLen,
				"jwt_iat", iat,
				"jwt_exp", exp,
				"jar_toks", jarToks,
			)
			return true, "", nil
		}

		// Second fallback (production only): probe the same-origin console proxy
		// https://user.vulbox.com/api/web/account/basic-info in case vapi.vulbox.com
		// rejects Go's direct request while the console proxy accepts it.
		var userAPILogged bool
		var userAPIErr string
		if c.user != nil && strings.EqualFold(apiHost, "vapi.vulbox.com") {
			if userAPIBase, parseErr := url.Parse(c.user.String() + "/api"); parseErr == nil {
				var uinfo basicInfoResp
				uerr := c.getJSONAtBaseNoRenew(ctx, userAPIBase, "/web/account/basic-info", nil, &uinfo)
				userAPILogged = uerr == nil && uinfo.Logged.Bool()
				if userAPILogged {
					c.setAPIBase(userAPIBase)
					if c.authenticated() {
						c.cred.Accepted()
						c.updateTokenExpiry()
					}
					c.log.Info("vulbox: session accepted via user.vulbox.com/api fallback",
						"jwt_len", tokLen,
						"jwt_iat", iat,
						"jwt_exp", exp,
						"jar_toks", c.jarTokenCount(),
					)
					return true, "", nil
				}
				if uerr != nil {
					userAPIErr = uerr.Error()
				}
			}
		}

		reason := "basic-info logged=false"
		if herr != nil {
			reason = fmt.Sprintf("basic-info logged=false; user/info: %v", herr)
		}
		c.log.Warn("vulbox: session not accepted",
			"reason", reason,
			"jwt_len", tokLen,
			"jwt_iat", iat,
			"jwt_exp", exp,
			"jar_toks", jarToks,
			"api_host", apiHost,
			"has_uuid", hasUUID,
			"user_api_logged", userAPILogged,
			"user_api_err", userAPIErr,
		)
		return false, reason, nil
	}

	c.log.Warn("vulbox: session not accepted",
		"reason", "empty vulbox_token and logged=false",
		"jwt_len", tokLen,
		"jar_toks", jarToks,
		"api_host", apiHost,
		"has_uuid", hasUUID,
	)
	return false, "empty vulbox_token and logged=false", nil
}

func (c *Client) apiBase() *url.URL {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	if c.apiBaseURL != nil {
		return c.apiBaseURL
	}
	return c.base
}

func (c *Client) setAPIBase(u *url.URL) {
	if u == nil {
		return
	}
	c.stateMu.Lock()
	c.apiBaseURL = u
	c.stateMu.Unlock()
}

func (c *Client) setToken(tok string) {
	c.stateMu.Lock()
	c.token = strings.TrimSpace(tok)
	c.stateMu.Unlock()
}

func (c *Client) currentUserAgent() string {
	c.stateMu.RLock()
	ua := c.userAgent
	c.stateMu.RUnlock()
	if ua != "" {
		return ua
	}
	return vulboxUserAgent
}

func (c *Client) setUserAgent(ua string) {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return
	}
	c.stateMu.Lock()
	c.userAgent = ua
	c.stateMu.Unlock()
}

func (c *Client) jarTokenCount() int {
	base := c.apiBase()
	if c.jar == nil || base == nil {
		return 0
	}
	n := 0
	for _, ck := range c.jar.Cookies(base) {
		if ck.Name == tokenCookie && ck.Value != "" {
			n++
		}
	}
	return n
}

func (c *Client) targetURLs() []*url.URL {
	candidates := []*url.URL{c.apiBase(), c.base, c.web, c.user, c.root}
	seen := make(map[string]bool, len(candidates))
	out := make([]*url.URL, 0, len(candidates))
	for _, u := range candidates {
		if u == nil {
			continue
		}
		key := u.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, u)
	}
	return out
}

// seedCookies puts cookies into the jar for all VulBox hosts. When seeding a
// new or deleted vulbox_token, it first expires any prior host-only or
// .vulbox.com vulbox_token entries so Go never sends duplicate Cookie headers.
func (c *Client) seedCookies(cookies []*http.Cookie) {
	var latestTok string
	var sawTok, sawDelete bool
	for _, ck := range cookies {
		if ck.Name == tokenCookie {
			v := strings.TrimSpace(ck.Value)
			if ck.MaxAge < 0 || v == "" {
				sawDelete = true
				sawTok = false
				latestTok = ""
			} else {
				sawTok = true
				latestTok = v
			}
		}
	}
	if sawTok {
		c.setToken(latestTok)
	} else if sawDelete {
		c.setToken("")
	}

	urls := c.targetURLs()
	if sawTok || sawDelete {
		clearTok := []*http.Cookie{
			{Name: tokenCookie, Path: "/", MaxAge: -1},                       //nolint:gosec // expire stale host-only cookie
			{Name: tokenCookie, Domain: "vulbox.com", Path: "/", MaxAge: -1}, //nolint:gosec // expire stale domain cookie
		}
		for _, u := range urls {
			c.jar.SetCookies(u, clearTok)
		}
	}

	for _, u := range urls {
		clones := make([]*http.Cookie, len(cookies))
		for i, ck := range cookies {
			cp := *ck
			cp.Domain = ""
			if cp.Path == "" {
				cp.Path = "/"
			}
			clones[i] = &cp
		}
		c.jar.SetCookies(u, clones)
	}
}

// currentToken returns the active vulbox_token, preferring the explicitly
// synced token and falling back to scanning all VulBox jar origins (returning
// the last non-empty value if multiple exist).
func (c *Client) currentToken() string {
	c.stateMu.RLock()
	tok := c.token
	c.stateMu.RUnlock()
	if tok != "" {
		return tok
	}
	var last string
	for _, u := range []*url.URL{c.apiBase(), c.base, c.user, c.web, c.root} {
		if u == nil {
			continue
		}
		for _, ck := range c.jar.Cookies(u) {
			if ck.Name == tokenCookie && strings.TrimSpace(ck.Value) != "" {
				last = strings.TrimSpace(ck.Value)
			}
		}
		if last != "" {
			return last
		}
	}
	return ""
}

// currentUUID returns the uuid cookie if set in the jar, else the generated client UUID.
func (c *Client) currentUUID() string {
	for _, u := range []*url.URL{c.base, c.user, c.web, c.root} {
		if u == nil {
			continue
		}
		for _, ck := range c.jar.Cookies(u) {
			if ck.Name == uuidCookie && ck.Value != "" {
				return ck.Value
			}
		}
	}
	return c.clientUUID
}

// updateTokenExpiry extracts the exp claim from the active vulbox_token JWT
// without verifying signatures or logging token material.
func (c *Client) updateTokenExpiry() {
	if exp := parseJWTExpiry(c.currentToken()); exp != nil {
		c.cred.ExpiresAt(*exp)
	}
}

func (c *Client) setMeta(handle string, fn func(*projectMeta)) {
	c.metaMu.Lock()
	defer c.metaMu.Unlock()
	m := c.meta[handle]
	fn(&m)
	c.meta[handle] = m
}

func (c *Client) getMeta(handle string) (projectMeta, bool) {
	c.metaMu.Lock()
	defer c.metaMu.Unlock()
	m, ok := c.meta[handle]
	return m, ok
}
