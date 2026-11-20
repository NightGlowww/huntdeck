// Package hackerone implements platform.Platform with the official
// HackerOne Hacker API (https://api.hackerone.com/hacker-resources/), and
// platform.StatsFetcher with the unofficial GraphQL endpoint behind
// hackerone.com (see stats.go).
//
// Requests per poll: one per page of 100 programs, plus one per program.
// Scope is read inline from GET /hackers/programs/{handle}, which falls under
// the general 600 reads/minute limit. The dedicated structured_scopes
// endpoint is limited to 50 requests/minute (since 2026-06-22), so it is only
// used when the inline list is truncated: about 1% of programs, but they
// can have thousands of entries (one request per 100).
//
// Program stats: one GraphQL request per three programs, at most one per
// second, so about 200 requests for 600 programs.
//
// Disclosed reports (hacktivity.go): every poll reads the newest page or so
// of Hacktivity; a program's full history costs one request per 50 reports,
// fetched once per program.
package hackerone

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// Name is the platform identifier stored in the database.
const Name = "hackerone"

const (
	defaultBaseURL = "https://api.hackerone.com/v1"
	pageSize       = 100
	// inlineCap is where HackerOne truncates the inline scope list (observed
	// 2026-10: lists of 645 and 100 entries matched the paginated endpoint
	// exactly; every program with more stopped at 850). A list this long is
	// refetched from structured_scopes.
	inlineCap = 850
)

// ErrAuth means HackerOne rejected the credentials.
var ErrAuth = errors.New("hackerone: authentication failed; check H1_USERNAME and H1_TOKEN")

// Client is a HackerOne Hacker API client.
type Client struct {
	http          *httpclient.Client
	base          *url.URL
	auth          string
	scopesLimiter *rate.Limiter
	graphQL       string
	statsLimiter  *rate.Limiter
	token         platform.CredentialState
	account       string
	web           *webSession // nil without a website login

	invitesMu sync.Mutex
	invites   []webInvite // from the last listing (see invitePrograms)
	invitesAt time.Time
}

// Options configures a Client.
type Options struct {
	Username, Token string
	HTTP            *httpclient.Client
	BaseURL         string  // tests only; default is the production API
	ScopesPerMinute float64 // structured_scopes endpoint budget; default 45
	GraphQLURL      string  // tests only; default is hackerone.com's GraphQL endpoint
	StatsPerSecond  float64 // program stats requests; default 1

	// Email, Password and TOTPSecret sign in to the website (see web.go),
	// for private programs' stats and the pending invitations; WebHTTP
	// carries its cookies in Jar, and talks to the website only.
	Email, Password, TOTPSecret string
	WebHTTP                     *httpclient.Client
	Jar                         http.CookieJar
	WebURL                      string                          // tests only; default https://hackerone.com
	Browser                     *weblogin.Host                  // the login browser, shared with other platforms
	LoginState                  string                          // file keeping the login history
	Login                       func(ctx context.Context) error // tests only: replaces the browser login
}

// New returns a Client.
func New(o Options) (*Client, error) {
	if o.Username == "" || o.Token == "" {
		return nil, errors.New("hackerone: username and token are required")
	}
	if o.BaseURL == "" {
		o.BaseURL = defaultBaseURL
	}
	base, err := url.Parse(strings.TrimSuffix(o.BaseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("hackerone: base URL: %w", err)
	}
	if o.ScopesPerMinute <= 0 {
		o.ScopesPerMinute = 45
	}
	if o.GraphQLURL == "" {
		o.GraphQLURL = defaultGraphQLURL
	}
	if o.StatsPerSecond <= 0 {
		o.StatsPerSecond = 1
	}
	c := &Client{
		http:          o.HTTP,
		base:          base,
		auth:          "Basic " + base64.StdEncoding.EncodeToString([]byte(o.Username+":"+o.Token)),
		scopesLimiter: rate.NewLimiter(rate.Limit(o.ScopesPerMinute/60), 1),
		graphQL:       o.GraphQLURL,
		statsLimiter:  rate.NewLimiter(rate.Limit(o.StatsPerSecond), 1),
		account:       strings.ToLower(o.Username),
	}
	if o.Email != "" || o.Password != "" {
		if o.Email == "" || o.Password == "" || o.Jar == nil || o.WebHTTP == nil {
			return nil, errors.New("hackerone: a website login needs the email, the password, a cookie jar and its HTTP client")
		}
		if o.WebURL == "" {
			o.WebURL = defaultWebURL
		}
		web, err := url.Parse(strings.TrimSuffix(o.WebURL, "/"))
		if err != nil {
			return nil, fmt.Errorf("hackerone: website URL: %w", err)
		}
		c.web = &webSession{account: &weblogin.Account{Who: who, Email: o.Email, Password: o.Password, TOTPSecret: o.TOTPSecret, TOTPEnv: "H1_TOTP_SECRET"},
			host: o.Browser, history: weblogin.NewHistory(o.LoginState), jar: o.Jar, http: o.WebHTTP, base: web, login: o.Login, now: time.Now}
		if c.web.login == nil {
			if o.Browser == nil {
				return nil, errors.New("hackerone: a website login needs the login browser")
			}
			c.web.login = c.browserLogin
		}
	}
	return c, nil
}

// Name implements platform.Platform.
func (c *Client) Name() string { return Name }

// Account implements platform.AccountNamer: the username.
func (c *Client) Account() string { return c.account }

// Stats implements platform.Platform.
func (c *Client) Stats() (int64, int64) { return c.http.Stats() }

type programJSON struct {
	ID         string `json:"id"`
	Attributes struct {
		Handle                 string   `json:"handle"`
		Name                   string   `json:"name"`
		Currency               string   `json:"currency"`
		Policy                 *string  `json:"policy"`
		State                  string   `json:"state"`
		SubmissionState        string   `json:"submission_state"`
		OffersBounties         bool     `json:"offers_bounties"`
		FastPayments           bool     `json:"fast_payments"`
		GoldStandardSafeHarbor bool     `json:"gold_standard_safe_harbor"`
		BountySplitting        *bool    `json:"allows_bounty_splitting"`
		TriageActive           *bool    `json:"triage_active"`
		OpenScope              *bool    `json:"open_scope"`
		StartedAcceptingAt     *string  `json:"started_accepting_at"`
		LastInvitationAccepted *string  `json:"last_invitation_accepted_at_for_user"`
		NumberOfReports        *int     `json:"number_of_reports_for_user"`
		NumberOfValidReports   *int     `json:"number_of_valid_reports_for_user"`
		BountyEarnedForUser    *float64 `json:"bounty_earned_for_user"`
	} `json:"attributes"`
}

type scopeJSON struct {
	ID         string `json:"id"`
	Attributes struct {
		AssetType             string  `json:"asset_type"`
		AssetIdentifier       string  `json:"asset_identifier"`
		EligibleForBounty     bool    `json:"eligible_for_bounty"`
		EligibleForSubmission bool    `json:"eligible_for_submission"`
		Instruction           *string `json:"instruction"`
		MaxSeverity           *string `json:"max_severity"`
		CreatedAt             *string `json:"created_at"`
		UpdatedAt             *string `json:"updated_at"`
		// CVSS environmental requirements, e.g. "high" or "not_defined".
		Confidentiality *string `json:"confidentiality_requirement"`
		Integrity       *string `json:"integrity_requirement"`
		Availability    *string `json:"availability_requirement"`
	} `json:"attributes"`
}

type page struct {
	Data  []json.RawMessage `json:"data"`
	Links struct {
		Next string `json:"next"`
	} `json:"links"`
}

// ListPrograms implements platform.Platform.
func (c *Client) ListPrograms(ctx context.Context) (platform.Listing, error) {
	var out platform.Listing
	next := c.endpoint(fmt.Sprintf("/hackers/programs?page%%5Bsize%%5D=%d", pageSize))
	for pageNo := 1; next != ""; pageNo++ {
		var p page
		if err := c.getJSON(ctx, next, &p); err != nil {
			return out, fmt.Errorf("hackerone: list programs page %d: %w", pageNo, err)
		}
		for _, raw := range p.Data {
			prog, err := parseProgram(raw)
			if err != nil {
				return out, fmt.Errorf("hackerone: list programs page %d: %w", pageNo, err)
			}
			out.Programs = append(out.Programs, prog)
		}
		var err error
		if next, err = c.sameHost(p.Links.Next); err != nil {
			return out, fmt.Errorf("hackerone: list programs page %d: %w", pageNo, err)
		}
	}
	if c.loggedIn() {
		// Invited programs aren't in the API's list. Without them the
		// listing isn't whole: an invited program missing from it must not
		// be taken as gone.
		invited, err := c.invitePrograms(ctx)
		if err != nil {
			return out, nil //nolint:nilerr // a partial listing; the login's failure shows in Settings
		}
		listed := make(map[string]bool, len(out.Programs))
		for _, p := range out.Programs {
			listed[p.Handle] = true
		}
		for _, p := range invited {
			if !listed[p.Handle] {
				out.Programs = append(out.Programs, p)
			}
		}
	}
	out.Complete = true
	return out, nil
}

// FetchScope implements platform.Platform.
func (c *Client) FetchScope(ctx context.Context, p platform.Program) ([]platform.ScopeEntry, error) {
	var single struct {
		Relationships struct {
			StructuredScopes *struct {
				Data []scopeJSON `json:"data"`
			} `json:"structured_scopes"`
		} `json:"relationships"`
	}
	if err := c.getJSON(ctx, c.endpoint("/hackers/programs/"+url.PathEscape(p.Handle)), &single); err != nil {
		return nil, fmt.Errorf("hackerone: program %s: %w", p.Handle, err)
	}
	ss := single.Relationships.StructuredScopes
	if ss == nil {
		return nil, fmt.Errorf("hackerone: program %s: response has no structured_scopes", p.Handle)
	}
	if len(ss.Data) < inlineCap {
		return convertScopes(ss.Data), nil
	}
	return c.fetchScopesPaginated(ctx, p.Handle)
}

// fetchScopesPaginated walks structured_scopes with an ID cursor. Page
// numbers stop working after 10,000 entries; filter[id__gt] doesn't, and
// results come back in ascending ID order.
func (c *Client) fetchScopesPaginated(ctx context.Context, handle string) ([]platform.ScopeEntry, error) {
	var all []scopeJSON
	var after int64
	for pageNo := 1; ; pageNo++ {
		if err := c.scopesLimiter.Wait(ctx); err != nil {
			return nil, err
		}
		var p struct {
			Data []scopeJSON `json:"data"`
		}
		u := c.endpoint(fmt.Sprintf("/hackers/programs/%s/structured_scopes?page%%5Bsize%%5D=%d&filter%%5Bid__gt%%5D=%d",
			url.PathEscape(handle), pageSize, after))
		if err := c.getJSON(ctx, u, &p); err != nil {
			return nil, fmt.Errorf("hackerone: program %s scopes page %d: %w", handle, pageNo, err)
		}
		all = append(all, p.Data...)
		if len(p.Data) < pageSize {
			return convertScopes(all), nil
		}
		last, err := strconv.ParseInt(p.Data[len(p.Data)-1].ID, 10, 64)
		if err != nil || last <= after {
			return nil, fmt.Errorf("hackerone: program %s scopes page %d: cursor did not advance", handle, pageNo)
		}
		after = last
	}
}

func (c *Client) getJSON(ctx context.Context, u string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(ctx, req)
	if err != nil {
		return err
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		c.token.Refused("HackerOne refused it (401): check H1_USERNAME and H1_TOKEN", false)
		return ErrAuth
	case res.StatusCode != http.StatusOK:
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	c.token.Accepted()
	if err := json.Unmarshal(res.Body, into); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// Credentials reports the API token's state, and the website login's;
// see platform.CredentialReporter.
func (c *Client) Credentials() []platform.Credential {
	out := []platform.Credential{c.token.Report("API token")}
	if c.web != nil {
		out = append(out, c.web.cred.Report("Website login"))
	}
	return out
}

func (c *Client) endpoint(pathAndQuery string) string {
	return c.base.String() + pathAndQuery
}

// sameHost validates a pagination link before we send credentials to it.
func (c *Client) sameHost(next string) (string, error) {
	if next == "" {
		return "", nil
	}
	u, err := url.Parse(next)
	if err != nil {
		return "", fmt.Errorf("bad pagination link: %w", err)
	}
	if u.Scheme != c.base.Scheme || u.Host != c.base.Host {
		return "", fmt.Errorf("pagination link points to unexpected host %q", u.Host)
	}
	return next, nil
}

func parseProgram(raw json.RawMessage) (platform.Program, error) {
	var pj programJSON
	if err := json.Unmarshal(raw, &pj); err != nil {
		return platform.Program{}, fmt.Errorf("decode program: %w", err)
	}
	a := pj.Attributes
	if a.Handle == "" {
		return platform.Program{}, errors.New("program without handle")
	}
	p := platform.Program{
		Handle:       a.Handle,
		PlatformID:   pj.ID,
		Name:         a.Name,
		URL:          "https://hackerone.com/" + a.Handle,
		Private:      a.State != "public_mode",
		State:        programState(a.SubmissionState),
		OffersBounty: a.OffersBounties,
		Currency:     strings.ToUpper(a.Currency),
		FastPayments: a.FastPayments,
		SafeHarbor:   a.GoldStandardSafeHarbor,
		TriageActive: a.TriageActive != nil && *a.TriageActive,
		OpenScope:    a.OpenScope != nil && *a.OpenScope,
		StartedAt:    parseTime(a.StartedAcceptingAt),
		JoinedAt:     parseTime(a.LastInvitationAccepted),
		Policy:       strings.TrimSpace(deref(a.Policy)),
		Raw:          raw,

		BountySplitting: a.BountySplitting,
	}
	if a.NumberOfReports != nil || a.NumberOfValidReports != nil || a.BountyEarnedForUser != nil {
		p.UserStats = &platform.UserStats{
			Reports:      deref(a.NumberOfReports),
			ValidReports: deref(a.NumberOfValidReports),
			BountyEarned: deref(a.BountyEarnedForUser),
		}
	}
	return p, nil
}

func programState(submissionState string) string {
	switch submissionState {
	case "open":
		return platform.StateOpen
	case "paused":
		return platform.StatePaused
	default:
		return platform.StateClosed
	}
}

func convertScopes(in []scopeJSON) []platform.ScopeEntry {
	out := make([]platform.ScopeEntry, 0, len(in))
	for _, s := range in {
		a := s.Attributes
		out = append(out, platform.ScopeEntry{
			PlatformID:        s.ID,
			InScope:           a.EligibleForSubmission,
			AssetType:         assetType(a.AssetType),
			PlatformAssetType: a.AssetType,
			Identifier:        strings.TrimSpace(a.AssetIdentifier),
			BountyEligible:    a.EligibleForBounty,
			MaxSeverity:       deref(a.MaxSeverity),
			Instruction:       deref(a.Instruction),
			CreatedAt:         parseTime(a.CreatedAt),
			UpdatedAt:         parseTime(a.UpdatedAt),
			CVSS: platform.CVSSRequirements{
				Confidentiality: deref(a.Confidentiality),
				Integrity:       deref(a.Integrity),
				Availability:    deref(a.Availability),
			},
		})
	}
	return out
}

// assetType maps HackerOne asset types to normalised ones.
func assetType(h1 string) string {
	switch strings.ToUpper(h1) {
	case "WILDCARD":
		return scope.AssetWildcard
	case "DOMAIN":
		return scope.AssetDomain
	case "URL":
		return scope.AssetURL
	case "API":
		return scope.AssetAPI
	case "IP_ADDRESS":
		return scope.AssetIP
	case "CIDR":
		return scope.AssetCIDR
	case "GOOGLE_PLAY_APP_ID", "OTHER_APK":
		return scope.AssetAndroid
	case "APPLE_STORE_APP_ID", "OTHER_IPA", "TESTFLIGHT":
		return scope.AssetIOS
	case "WINDOWS_APP_STORE_APP_ID":
		return scope.AssetWindows
	case "SOURCE_CODE":
		return scope.AssetSourceCode
	case "DOWNLOADABLE_EXECUTABLES":
		return scope.AssetExecutable
	case "HARDWARE":
		return scope.AssetHardware
	case "SMART_CONTRACT":
		return scope.AssetSmartContract
	case "AI_MODEL":
		return scope.AssetAIModel
	default:
		return scope.AssetOther
	}
}

func parseTime(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil
	}
	t = t.UTC()
	return &t
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// Me reads the token's account.
