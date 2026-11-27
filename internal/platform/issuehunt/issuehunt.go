// Package issuehunt implements platform.Platform and optional HuntDeck
// interfaces for IssueHunt (https://issuehunt.io).
//
// IssueHunt exposes a JSON REST API at https://api.issuehunt.io. Public and
// application-only programs can be listed without credentials. Authenticated
// mode uses a User API Key (Authorization: Bearer <token>), verified via
// GET /users/authenticated, unlocking private invite/approved programs,
// personal reports (never /messages), and user reward activities.
package issuehunt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
)

// Name is the platform identifier stored in the database.
const Name = "issuehunt"

const (
	defaultBaseURL     = "https://api.issuehunt.io"
	defaultWebURL      = "https://issuehunt.io"
	defaultRate        = 1.5
	defaultConcurrency = 2
	currencyJPY        = "JPY"
	maxPages           = 200
)

// ErrAuth means IssueHunt rejected the configured API key (HTTP 401).
var ErrAuth = errors.New("issuehunt: API key rejected; set a fresh IH_API_KEY")

// Compile-time interface assertions.
var (
	_ platform.Platform           = (*Client)(nil)
	_ platform.ScopeDetailFetcher = (*Client)(nil)
	_ platform.CredentialReporter = (*Client)(nil)
	_ platform.AccountNamer       = (*Client)(nil)
	_ platform.ReportFetcher      = (*Client)(nil)
	_ platform.DisclosureFetcher  = (*Client)(nil)
	_ platform.StatsFetcher       = (*Client)(nil)
)

// Options configures an IssueHunt Client.
type Options struct {
	BaseURL     string  // default "https://api.issuehunt.io"
	WebURL      string  // default "https://issuehunt.io"
	APIKey      string  // User API Key (from IH_API_KEY)
	PublicOnly  bool    // list public programs without sending Authorization
	Rate        float64 // requests/second; default 1.5
	Concurrency int     // worker concurrency; default 2
	ProxyURL    string  // optional HTTP(S) proxy URL
	Log         *slog.Logger
}

// Client talks to the IssueHunt API. It is safe for concurrent use.
type Client struct {
	http        *httpclient.Client
	base        *url.URL
	web         *url.URL
	apiKey      string
	publicOnly  bool
	concurrency int
	log         *slog.Logger

	cred platform.CredentialState

	authMu     sync.Mutex
	userID     string
	uniqueName string

	cacheMu  sync.RWMutex
	programs map[string]programJSON // keyed by handle (slug) and platform ID
}

// New builds an IssueHunt Client.
func New(opts Options) (*Client, error) {
	baseRaw := strings.TrimSpace(opts.BaseURL)
	if baseRaw == "" {
		baseRaw = defaultBaseURL
	}
	base, err := url.Parse(strings.TrimSuffix(baseRaw, "/"))
	if err != nil {
		return nil, fmt.Errorf("issuehunt: base URL: %w", err)
	}

	webRaw := strings.TrimSpace(opts.WebURL)
	if webRaw == "" {
		webRaw = defaultWebURL
	}
	web, err := url.Parse(strings.TrimSuffix(webRaw, "/"))
	if err != nil {
		return nil, fmt.Errorf("issuehunt: web URL: %w", err)
	}

	var proxyURL *url.URL
	if raw := strings.TrimSpace(opts.ProxyURL); raw != "" {
		if proxyURL, err = url.Parse(raw); err != nil {
			return nil, fmt.Errorf("issuehunt: proxy URL: %w", err)
		}
	}

	rate := opts.Rate
	if rate <= 0 {
		rate = defaultRate
	}
	conc := opts.Concurrency
	if conc <= 0 {
		conc = defaultConcurrency
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	key := strings.TrimSpace(opts.APIKey)
	if len(key) > 7 && strings.EqualFold(key[:7], "Bearer ") {
		key = strings.TrimSpace(key[7:])
	}

	hc := httpclient.New(httpclient.Options{
		RatePerSecond: rate,
		Burst:         2,
		Proxy:         proxyURL,
	})

	return &Client{
		http:        hc,
		base:        base,
		web:         web,
		apiKey:      key,
		publicOnly:  opts.PublicOnly,
		concurrency: conc,
		log:         log,
		programs:    map[string]programJSON{},
	}, nil
}

// Name implements platform.Platform.
func (c *Client) Name() string { return Name }

// Stats implements platform.Platform.
func (c *Client) Stats() (requests, rateLimited int64) {
	return c.http.Stats()
}

// authenticated reports whether the client is configured with an API key.
func (c *Client) authenticated() bool {
	return !c.publicOnly && c.apiKey != ""
}

// Credentials implements platform.CredentialReporter.
func (c *Client) Credentials() []platform.Credential {
	if !c.authenticated() {
		return nil
	}
	return []platform.Credential{c.cred.Report("API key")}
}

// Account implements platform.AccountNamer.
func (c *Client) Account() string {
	if !c.authenticated() {
		return ""
	}
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.uniqueName != "" {
		return strings.ToLower(strings.TrimSpace(c.uniqueName))
	}
	return c.userID
}

type authUserResp struct {
	Data struct {
		ID          string `json:"id"`
		UniqueName  string `json:"uniqueName"`
		DisplayName string `json:"displayName"`
	} `json:"data"`
}

// ensureAuth verifies the API key via GET /users/authenticated and caches the
// authenticated user ID and handle.
func (c *Client) ensureAuth(ctx context.Context) (string, error) {
	if !c.authenticated() {
		return "", ErrAuth
	}
	c.authMu.Lock()
	if c.userID != "" {
		id := c.userID
		c.authMu.Unlock()
		return id, nil
	}
	c.authMu.Unlock()

	var res authUserResp
	if err := c.getJSON(ctx, "/users/authenticated", &res); err != nil {
		return "", err
	}
	id := strings.TrimSpace(res.Data.ID)
	if id == "" {
		c.cred.Refused("IssueHunt /users/authenticated returned empty user ID", true)
		return "", fmt.Errorf("%w: empty user ID", ErrAuth)
	}
	uname := firstNonEmpty(res.Data.UniqueName, res.Data.DisplayName, id)

	c.authMu.Lock()
	c.userID = id
	c.uniqueName = uname
	c.authMu.Unlock()
	c.cred.Accepted()
	return id, nil
}
