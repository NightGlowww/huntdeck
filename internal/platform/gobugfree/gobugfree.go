// Package gobugfree implements platform.Platform and optional interfaces for
// GObugfree (https://app.gobugfree.com).
//
// Three operating modes:
//   - Public-only (no credentials or PublicOnly=true): reads GET /public/engagements
//     and GET /public/engagements/{orgSlug}/{slug} without signing in.
//   - Token (Token / GBF_TOKEN): accepts a Bearer JWT access_token, "Bearer <jwt>",
//     a Keycloak refresh_token (auto-refreshed against IDURL), or a "session=<cookie>"
//     value, sending Accept: application/json, Authorization: Bearer (when held),
//     and cookies via Jar.
//   - Website login (Email + Password + optional TOTPSecret via WebHost): signs in
//     through Keycloak (id.gobugfree.com) in the shared login browser, captures the
//     session cookie into Jar (*sessionjar.Jar), and falls back to RFC 8628 device
//     authorization (client_id=agent) if cookie-only requests return HTML.
package gobugfree

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// Name is the platform identifier stored in the database.
const Name = "gobugfree"

// WebHome is the signed-in dashboard opened by the shared login browser.
const WebHome = "https://app.gobugfree.com/engagements"

const (
	defaultBaseURL     = "https://app.gobugfree.com"
	defaultIDURL       = "https://id.gobugfree.com/realms/gobugfree"
	defaultRate        = 1.5
	defaultConcurrency = 2
	defaultClientID    = "agent"
	sessionCookieName  = "session"
	pageSize           = 24
	reportPageSize     = 100
	maxPages           = 200
)

var (
	// ErrSessionExpired means GObugfree no longer accepts the token or session
	// and HuntDeck cannot renew it automatically.
	ErrSessionExpired = errors.New("gobugfree: session expired or invalid; set a fresh GBF_TOKEN or sign in via Settings > Browser")

	errNotFound = errors.New("gobugfree: resource not found")
)

// Compile-time interface assertions.
var (
	_ platform.Platform           = (*Client)(nil)
	_ platform.ScopeDetailFetcher = (*Client)(nil)
	_ platform.CredentialReporter = (*Client)(nil)
	_ platform.AccountNamer       = (*Client)(nil)
	_ platform.KeepAliver         = (*Client)(nil)
	_ platform.InviteLister       = (*Client)(nil)
	_ platform.InviteAccepter     = (*Client)(nil)
	_ platform.ReportFetcher      = (*Client)(nil)
	_ platform.StatsFetcher       = (*Client)(nil)
)

// Options configures a GObugfree Client.
type Options struct {
	BaseURL     string  // default "https://app.gobugfree.com"
	IDURL       string  // default "https://id.gobugfree.com/realms/gobugfree"
	Email       string  // from GBF_EMAIL
	Password    string  // from GBF_PASSWORD
	TOTPSecret  string  // optional base32 TOTP (from GBF_TOTP_SECRET)
	Token       string  // optional Bearer JWT, refresh_token, or session=<cookie> (from GBF_TOKEN)
	PublicOnly  bool    // from GBF_PUBLIC_ONLY
	Rate        float64 // default 1.5
	Concurrency int     // default 2
	ProxyURL    string
	Jar         http.CookieJar // *sessionjar.Jar for persistence + Checkpoint/Rollback
	LoginState  string         // path to sessions/gobugfree-login.json
	WebHost     *weblogin.Host
	Log         *slog.Logger
}

// Client talks to GObugfree. It is safe for concurrent use.
type Client struct {
	http   *httpclient.Client // retries network/5xx/429 for safe GETs
	action *httpclient.Client // MaxAttempts: 1 for state-changing POSTs
	jar    http.CookieJar

	base *url.URL // app.gobugfree.com (or httptest server)
	id   *url.URL // id.gobugfree.com/realms/gobugfree (or httptest server)

	email       string
	password    string
	totpSecret  string
	publicOnly  bool
	hasToken    bool
	concurrency int
	log         *slog.Logger

	host    *weblogin.Host
	account *weblogin.Account
	logins  *weblogin.History

	// Test hooks.
	now            func() time.Time
	sleep          func(context.Context, time.Duration) error
	browserLoginFn func(context.Context) error
	deviceLoginFn  func(context.Context, string) error

	authMu sync.Mutex
	cred   platform.CredentialState

	stateMu       sync.RWMutex
	accessToken   string
	refreshToken  string
	accountHandle string

	metaMu sync.Mutex
	meta   map[string]engagementMeta // handle -> metadata learned during listing/invites
}

// engagementMeta caches listing/invite fields needed by resolveOrgSlug and FetchScopeDetail.
type engagementMeta struct {
	OrgSlug    string
	Slug       string
	Visibility string
	Invited    bool
	Member     bool
	Locked     bool
	Title      string
}

// New builds a GObugfree Client.
func New(opts Options) (*Client, error) {
	email := strings.TrimSpace(opts.Email)
	if !opts.PublicOnly && (email == "") != (opts.Password == "") {
		return nil, errors.New("gobugfree: email and password must be set together")
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
		totpSecret:  opts.TOTPSecret,
		publicOnly:  opts.PublicOnly,
		concurrency: concurrency,
		log:         log,
		now:         time.Now,
		sleep:       sleepCtx,
		meta:        map[string]engagementMeta{},
	}
	c.browserLoginFn = c.browserLogin
	c.deviceLoginFn = c.runDeviceFlowBrowser

	var err error
	baseRaw := strings.TrimSuffix(strings.TrimSpace(opts.BaseURL), "/")
	if baseRaw == "" {
		baseRaw = defaultBaseURL
	}
	if c.base, err = url.Parse(baseRaw); err != nil {
		return nil, fmt.Errorf("gobugfree: base URL: %w", err)
	}

	idRaw := strings.TrimSuffix(strings.TrimSpace(opts.IDURL), "/")
	if idRaw == "" {
		idRaw = defaultIDURL
	}
	if c.id, err = url.Parse(idRaw); err != nil {
		return nil, fmt.Errorf("gobugfree: id URL: %w", err)
	}

	var proxyURL *url.URL
	if p := strings.TrimSpace(opts.ProxyURL); p != "" {
		if proxyURL, err = url.Parse(p); err != nil {
			return nil, fmt.Errorf("gobugfree: proxy URL: %w", err)
		}
	}

	if opts.Jar != nil {
		c.jar = opts.Jar
	} else if c.jar, err = cookiejar.New(nil); err != nil {
		return nil, err
	}

	if !opts.PublicOnly && email != "" {
		c.host = opts.WebHost
		c.logins = weblogin.NewHistory(opts.LoginState)
		c.logins.Now = c.now
		c.account = &weblogin.Account{
			Who:        who,
			Email:      email,
			Password:   opts.Password,
			TOTPSecret: opts.TOTPSecret,
			TOTPEnv:    "GBF_TOTP_SECRET",
			Now:        c.now,
		}
	}

	if !opts.PublicOnly && strings.TrimSpace(opts.Token) != "" {
		if err := c.applyTokenInput(opts.Token); err != nil {
			return nil, err
		}
		c.hasToken = true
		c.updateTokenExpiry()
	}

	ho := httpclient.Options{
		RatePerSecond: rate,
		Burst:         2,
		Proxy:         proxyURL,
		Jar:           c.jar,
		RedirectHosts: []string{c.base.Host},
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
	if c.email != "" {
		return strings.ToLower(c.email)
	}
	c.stateMu.RLock()
	h := c.accountHandle
	c.stateMu.RUnlock()
	return strings.ToLower(strings.TrimSpace(h))
}

// Stats implements platform.Platform.
func (c *Client) Stats() (requests, rateLimited int64) {
	r1, l1 := c.http.Stats()
	r2, l2 := c.action.Stats()
	return r1 + r2, l1 + l2
}

func (c *Client) authenticated() bool {
	if c.publicOnly {
		return false
	}
	return c.hasToken || c.email != ""
}

func (c *Client) canBrowserLogin() bool {
	return !c.publicOnly && c.email != "" && c.host != nil
}

// Credentials implements platform.CredentialReporter.
// The name "Website login" is required for Settings > Browser VNC links.
func (c *Client) Credentials() []platform.Credential {
	if c.publicOnly {
		return nil
	}
	switch {
	case c.email != "":
		return []platform.Credential{c.cred.Report("Website login")}
	case c.hasToken:
		return []platform.Credential{c.cred.Report("API token")}
	default:
		return nil
	}
}

// KeepAlive implements platform.KeepAliver.
func (c *Client) KeepAlive(ctx context.Context) error {
	if !c.authenticated() {
		return nil
	}
	if err := c.ensureSession(ctx); err != nil {
		return err
	}
	ok, reason, err := c.sessionCheck(ctx)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	if err := c.renewSession(ctx); err != nil {
		return err
	}
	ok, reason2, err := c.sessionCheck(ctx)
	if err != nil {
		return err
	}
	if !ok {
		if reason2 != "" {
			reason = reason2
		}
		if !c.canBrowserLogin() {
			c.cred.Refused("GObugfree no longer accepts the session ("+reason+"): set a fresh GBF_TOKEN", true)
		}
		return ErrSessionExpired
	}
	return nil
}

// ensureSession makes sure usable authentication material is held before
// calling authenticated endpoints.
func (c *Client) ensureSession(ctx context.Context) error {
	if !c.authenticated() {
		return platform.ErrNoWebLogin
	}
	c.authMu.Lock()
	defer c.authMu.Unlock()

	access, refresh := c.tokens()
	if refresh != "" && (access == "" || c.isTokenExpiringSoon(access)) {
		if err := c.refreshAccessTokenLocked(ctx); err == nil {
			return nil
		} else if !c.canBrowserLogin() {
			return err
		}
	}

	if access != "" || c.hasSessionCookie() {
		return nil
	}

	if !c.canBrowserLogin() {
		if c.email != "" {
			c.cred.Refused("GObugfree website login requires the login browser or GBF_TOKEN", false)
		} else {
			c.cred.Refused("GObugfree session expired or missing: set a fresh GBF_TOKEN", true)
		}
		return ErrSessionExpired
	}
	return c.browserSignInLocked(ctx)
}

// renewSession refreshes an expired token or re-runs browser sign-in after an
// authenticated API call receives 401/302/HTML.
func (c *Client) renewSession(ctx context.Context) error {
	c.authMu.Lock()
	defer c.authMu.Unlock()

	_, refresh := c.tokens()
	if refresh != "" {
		if err := c.refreshAccessTokenLocked(ctx); err == nil {
			return nil
		} else if !c.canBrowserLogin() {
			return err
		}
	}

	if !c.canBrowserLogin() {
		c.cred.Refused("GObugfree session expired or invalid (401): set a fresh GBF_TOKEN", true)
		return ErrSessionExpired
	}
	return c.browserSignInLocked(ctx)
}

type settingsResp struct {
	UserID      int64  `json:"userId"`
	Handle      string `json:"handle"`
	DisplayName string `json:"displayName"`
	Country     string `json:"country"`
	IsPublic    bool   `json:"isPublic"`
}

func (c *Client) sessionCheck(ctx context.Context) (bool, string, error) {
	var info settingsResp
	err := c.getJSONNoRenew(ctx, "/settings", &info)
	if err != nil {
		if errors.Is(err, ErrSessionExpired) {
			return false, err.Error(), nil
		}
		return false, err.Error(), err
	}
	if info.UserID <= 0 && strings.TrimSpace(info.Handle) == "" {
		return false, "settings response missing userId/handle", nil
	}
	if h := strings.TrimSpace(info.Handle); h != "" {
		c.stateMu.Lock()
		c.accountHandle = h
		c.stateMu.Unlock()
	}
	if c.authenticated() {
		c.cred.Accepted()
		c.updateTokenExpiry()
	}
	return true, "", nil
}

// applyTokenInput parses GBF_TOKEN into accessToken, refreshToken, or session cookies.
func (c *Client) applyTokenInput(raw string) error {
	s := strings.TrimSpace(raw)
	if s == "" {
		return errors.New("gobugfree: GBF_TOKEN is empty")
	}
	if len(s) > 7 && strings.EqualFold(s[:7], "Bearer ") {
		tok := strings.TrimSpace(s[7:])
		if tok == "" {
			return errors.New("gobugfree: GBF_TOKEN Bearer value is empty")
		}
		return c.classifyAndStoreToken(tok)
	}
	if len(s) > 7 && strings.EqualFold(s[:7], "Cookie:") {
		s = strings.TrimSpace(s[7:])
	}
	if strings.HasPrefix(s, "refresh_token=") {
		rt := strings.TrimSpace(strings.TrimPrefix(s, "refresh_token="))
		if rt == "" {
			return errors.New("gobugfree: GBF_TOKEN refresh_token value is empty")
		}
		c.setTokens("", rt)
		return nil
	}
	if strings.HasPrefix(s, sessionCookieName+"=") || (strings.Contains(s, "=") && strings.Contains(s, ";")) {
		cs, err := http.ParseCookie(s)
		if err != nil || len(cs) == 0 {
			return errors.New("gobugfree: GBF_TOKEN cookie value is invalid")
		}
		c.seedBaseCookies(cs)
		return nil
	}
	// Try parsing cookie form only if it explicitly names "session=".
	if strings.Contains(s, "=") {
		if cs, err := http.ParseCookie(s); err == nil {
			for _, ck := range cs {
				if ck.Name == sessionCookieName && ck.Value != "" {
					c.seedBaseCookies(cs)
					return nil
				}
			}
		}
	}
	return c.classifyAndStoreToken(s)
}

func (c *Client) classifyAndStoreToken(tok string) error {
	typ, sub := parseJWTClaims(tok)
	if sub != "" {
		c.stateMu.Lock()
		if c.accountHandle == "" {
			c.accountHandle = sub
		}
		c.stateMu.Unlock()
	}
	if strings.EqualFold(typ, "Refresh") || strings.EqualFold(typ, "Offline") {
		c.setTokens("", tok)
		return nil
	}
	c.setTokens(tok, "")
	return nil
}

func (c *Client) seedBaseCookies(cookies []*http.Cookie) {
	if c.jar == nil || c.base == nil || len(cookies) == 0 {
		return
	}
	clones := make([]*http.Cookie, 0, len(cookies))
	for _, ck := range cookies {
		if ck == nil {
			continue
		}
		cp := *ck //nolint:gosec // client-side cookie jar clone preserves original attributes (including httptest HTTP URLs)
		cp.Domain = ""
		if cp.Path == "" {
			cp.Path = "/"
		}
		clones = append(clones, &cp)
	}
	c.jar.SetCookies(c.base, clones)
}

func (c *Client) hasSessionCookie() bool {
	if c.jar == nil || c.base == nil {
		return false
	}
	for _, ck := range c.jar.Cookies(c.base) {
		if ck.Name == sessionCookieName && strings.TrimSpace(ck.Value) != "" {
			return true
		}
	}
	return false
}

func (c *Client) tokens() (access, refresh string) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.accessToken, c.refreshToken
}

func (c *Client) setTokens(access, refresh string) {
	c.stateMu.Lock()
	c.accessToken = strings.TrimSpace(access)
	c.refreshToken = strings.TrimSpace(refresh)
	c.stateMu.Unlock()
}

func (c *Client) currentAccessToken() string {
	c.stateMu.RLock()
	tok := c.accessToken
	c.stateMu.RUnlock()
	return tok
}

func (c *Client) isTokenExpiringSoon(tok string) bool {
	exp := parseJWTExpiry(tok)
	if exp == nil {
		return false
	}
	return !exp.After(c.now().Add(30 * time.Second))
}

func (c *Client) updateTokenExpiry() {
	access, refresh := c.tokens()
	if refresh != "" {
		if exp := parseJWTExpiry(refresh); exp != nil {
			c.cred.ExpiresAt(*exp)
			return
		}
	}
	if access != "" {
		if exp := parseJWTExpiry(access); exp != nil {
			c.cred.ExpiresAt(*exp)
		}
	}
}

// parseJWTMeta decodes the payload segment of a JWT and returns iat and exp
// Unix timestamps (0 when absent or invalid). Never logs token bytes.
func parseJWTMeta(tok string) (iat, exp int64) {
	parts := strings.Split(strings.TrimSpace(tok), ".")
	if len(parts) != 3 || parts[1] == "" {
		return 0, 0
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, 0
	}
	var claims struct {
		Iat float64 `json:"iat"`
		Exp float64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0, 0
	}
	return int64(claims.Iat), int64(claims.Exp)
}

func parseJWTClaims(tok string) (typ, user string) {
	parts := strings.Split(strings.TrimSpace(tok), ".")
	if len(parts) != 3 || parts[1] == "" {
		return "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var claims struct {
		Typ               string `json:"typ"`
		PreferredUsername string `json:"preferred_username"`
		Email             string `json:"email"`
		Sub               string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", ""
	}
	return strings.TrimSpace(claims.Typ), firstNonEmpty(claims.PreferredUsername, claims.Email, claims.Sub)
}

func parseJWTExpiry(tok string) *time.Time {
	_, sec := parseJWTMeta(tok)
	if sec <= 0 {
		return nil
	}
	t := time.Unix(sec, 0).UTC()
	return &t
}

// resolveOrgSlug extracts (orgSlug, engagementSlug) from cached metadata,
// p.PlatformID ("org/slug"), or p.Handle ("org--slug").
func (c *Client) resolveOrgSlug(p platform.Program) (orgSlug, slug string, err error) {
	if p.Handle != "" {
		if m, ok := c.getMeta(p.Handle); ok && m.OrgSlug != "" && m.Slug != "" {
			return m.OrgSlug, m.Slug, nil
		}
	}
	if o, s, ok := strings.Cut(strings.TrimSpace(p.PlatformID), "/"); ok && o != "" && s != "" {
		return o, s, nil
	}
	if o, s, ok := strings.Cut(strings.TrimSpace(p.Handle), "--"); ok && o != "" && s != "" {
		return o, s, nil
	}
	return "", "", fmt.Errorf("gobugfree: cannot resolve org/slug from handle %q", p.Handle)
}

func makeHandle(orgSlug, slug string) string {
	return orgSlug + "--" + slug
}

func makePlatformID(orgSlug, slug string) string {
	return orgSlug + "/" + slug
}

func (c *Client) engagementURL(orgSlug, slug string, public bool) string {
	prefix := "/engagements/"
	if public {
		prefix = "/public/engagements/"
	}
	return c.base.String() + prefix + url.PathEscape(orgSlug) + "/" + url.PathEscape(slug)
}

func (c *Client) setMeta(handle string, fn func(*engagementMeta)) {
	c.metaMu.Lock()
	defer c.metaMu.Unlock()
	m := c.meta[handle]
	fn(&m)
	c.meta[handle] = m
}

func (c *Client) getMeta(handle string) (engagementMeta, bool) {
	c.metaMu.Lock()
	defer c.metaMu.Unlock()
	m, ok := c.meta[handle]
	return m, ok
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func parseTime(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			u := t.UTC()
			return &u
		}
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
