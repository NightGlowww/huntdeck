// Package yeswehack implements platform.Platform with api.yeswehack.com, the
// API the YesWeHack website itself uses. It isn't documented for hunters,
// but it is plain JSON with bearer-token auth.
//
// Requests per poll: one per page of programs (2 for the 56 public ones in
// 2026-10), plus one per program whose scope is fetched. The listing's
// last_update_at becomes Program.ScopeChangedAt, so polls only fetch
// programs updated since their last download (and each at least daily).
// Full polls read each program's document again about daily for its stats.
//
// Without credentials only public programs are listed. With email and
// password (and TOTP secret, if 2FA is on) HuntDeck logs in and also sees
// private invites; it logs in again when the token expires. An invalid
// token gets HTTP 401, never a silently public listing.
package yeswehack

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/totp"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// Name is the platform identifier stored in the database.
const Name = "yeswehack"

const (
	defaultBaseURL = "https://api.yeswehack.com"
	webURL         = "https://yeswehack.com/programs/"
	maxPages       = 100
)

// errNotFound is a 404: no such program, or a part of it that isn't shown.
var errNotFound = errors.New("not found")

// ErrAuth means YesWeHack rejected the token and HuntDeck can't log in again.
var ErrAuth = errors.New("yeswehack: token rejected; set a fresh YWH_TOKEN or YWH_EMAIL/YWH_PASSWORD/YWH_TOTP_SECRET")

// Options configures a Client.
type Options struct {
	Email, Password, TOTPSecret string
	Token                       string // a bearer token copied from the browser
	HTTP                        *httpclient.Client

	// Browser, if set, is the shared login browser: email and password
	// logins run there (browserlogin.go), the token is taken from the web
	// app and renewed as it does, and saved in TokenFile (owner-only) so a
	// restart carries on with it. LoginState keeps the login history
	// (weblogin.History).
	Browser    *weblogin.Host
	TokenFile  string
	LoginState string

	// Tests only.
	BaseURL string
	Now     func() time.Time
	Sleep   func(context.Context, time.Duration) error
}

// Client is a YesWeHack client. It is safe for concurrent use.
type Client struct {
	http                        *httpclient.Client
	base                        *url.URL
	email, password, totpSecret string
	now                         func() time.Time
	sleep                       func(context.Context, time.Duration) error

	authMu       sync.Mutex
	token        string
	expires      time.Time // zero if unknown
	loginErr     error     // credentials were rejected: don't retry (lockout)
	lastTOTPStep int64
	cred         platform.CredentialState // the login, or the pasted token
	account      string                   // the login's email, else the pasted token

	// With the login browser: the shared Chrome, the account it signs in
	// with, how its logins went, and where the token is saved.
	host      *weblogin.Host
	web       *weblogin.Account
	logins    *weblogin.History
	tokenFile string
	saveErr   error // the token couldn't be saved (it's still used)
}

// Credentials reports the login's (or the pasted token's) state; none in
// public mode. See platform.CredentialReporter.
func (c *Client) Credentials() []platform.Credential {
	if c.email != "" && c.host != nil {
		return []platform.Credential{c.cred.Report("Website login")}
	}
	if c.email != "" {
		return []platform.Credential{c.cred.Report("Login")}
	}
	c.authMu.Lock()
	pasted := c.token != ""
	c.authMu.Unlock()
	if pasted {
		return []platform.Credential{c.cred.Report("Token")}
	}
	return nil
}

// New returns a Client. With no credentials it lists public programs only.
func New(o Options) (*Client, error) {
	if (o.Email == "") != (o.Password == "") {
		return nil, errors.New("yeswehack: email and password must be set together")
	}
	if o.BaseURL == "" {
		o.BaseURL = defaultBaseURL
	}
	base, err := url.Parse(strings.TrimSuffix(o.BaseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("yeswehack: base URL: %w", err)
	}
	c := &Client{
		http: o.HTTP, base: base, email: o.Email, password: o.Password, totpSecret: o.TOTPSecret,
		now: o.Now, sleep: o.Sleep, token: o.Token,
	}
	c.account = "token:" + o.Token
	if o.Email != "" {
		c.account = "login:" + strings.ToLower(o.Email)
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.sleep == nil {
		c.sleep = sleepCtx
	}
	c.expires = tokenExpiry(c.token)
	if o.Browser != nil && o.Email != "" {
		c.host, c.tokenFile, c.logins = o.Browser, o.TokenFile, weblogin.NewHistory(o.LoginState)
		c.logins.Now = c.now
		c.web = &weblogin.Account{Who: who, Email: o.Email, Password: o.Password, TOTPSecret: o.TOTPSecret,
			TOTPEnv: "YWH_TOTP_SECRET", Now: c.now}
		c.loadToken()
	}
	return c, nil
}

// Account implements platform.AccountNamer.
func (c *Client) Account() string { return c.account }

// Name implements platform.Platform.
func (c *Client) Name() string { return Name }

// Stats implements platform.Platform.
func (c *Client) Stats() (int64, int64) { return c.http.Stats() }

type programJSON struct {
	Slug         string `json:"slug"`
	Title        string `json:"title"`
	Public       bool   `json:"public"`
	Disabled     bool   `json:"disabled"`
	Archived     bool   `json:"archived"`
	Demo         bool   `json:"demo"`
	BusinessUnit struct {
		Slug string `json:"slug"`
	} `json:"business_unit"`
	Bounty       bool   `json:"bounty"`
	LastUpdateAt string `json:"last_update_at"`
	// The bounty range, in euros.
	BountyRewardMin float64 `json:"bounty_reward_min"`
	BountyRewardMax float64 `json:"bounty_reward_max"`
	// HunterAudience is "verified" for programs open only to
	// identity-verified hunters.
	HunterAudience string `json:"hunter_audience"`
	// ReportCost is the credits a report costs to submit (the reports'
	// cost_credits in the API documentation).
	ReportCost    *int   `json:"report_submission_cost"`
	Collaboration *bool  `json:"report_collaboration_active"`
	Country       string `json:"country"` // ISO code, e.g. "FR"
	Gift          *bool  `json:"gift"`    // the program gives swag
}

// ListPrograms implements platform.Platform.
func (c *Client) ListPrograms(ctx context.Context) (platform.Listing, error) {
	var out platform.Listing
	seen := map[string]bool{}
	listed := 0
	total, pages := -1, 1
	for page := 1; page <= pages; page++ {
		if page > maxPages {
			return out, fmt.Errorf("yeswehack: list programs: more than %d pages", maxPages)
		}
		var p struct {
			Items      []json.RawMessage `json:"items"`
			Pagination struct {
				NbPages   int `json:"nb_pages"`
				NbResults int `json:"nb_results"`
			} `json:"pagination"`
		}
		if err := c.getJSON(ctx, fmt.Sprintf("%s/programs?page=%d", c.base, page), &p); err != nil {
			return out, fmt.Errorf("yeswehack: list programs page %d: %w", page, err)
		}
		if total < 0 {
			total, pages = p.Pagination.NbResults, p.Pagination.NbPages
			if total <= 0 && len(p.Items) > 0 {
				return out, errors.New("yeswehack: list programs: response has no pagination.nb_results (format changed?)")
			}
		}
		for _, raw := range p.Items {
			listed++
			var pj programJSON
			if err := json.Unmarshal(raw, &pj); err != nil {
				return out, fmt.Errorf("yeswehack: list programs page %d: decode program: %w", page, err)
			}
			if pj.Slug == "" || strings.ContainsAny(pj.Slug, "/?#") {
				return out, fmt.Errorf("yeswehack: list programs page %d: program %q has unexpected slug %q", page, pj.Title, pj.Slug)
			}
			if pj.Demo || isDojo(pj) || seen[pj.Slug] {
				continue
			}
			seen[pj.Slug] = true
			out.Programs = append(out.Programs, convertProgram(pj, raw))
		}
	}
	if listed < total {
		return out, fmt.Errorf("yeswehack: listing returned %d of %d programs; it changed while paging", listed, total)
	}
	out.Complete = true
	return out, nil
}

// isDojo reports whether pj is YesWeHack's Dojo: monthly practice
// challenges listed as a program, whose thousands of solves would count as
// reports received and valid ones.
func isDojo(pj programJSON) bool {
	return pj.Slug == "dojo" || pj.BusinessUnit.Slug == "dojo-by-yes-we-hack"
}

func convertProgram(pj programJSON, raw json.RawMessage) platform.Program {
	p := platform.Program{
		Handle:       pj.Slug,
		Name:         strings.TrimSpace(pj.Title),
		URL:          webURL + pj.Slug,
		Private:      !pj.Public,
		State:        platform.StateOpen,
		OffersBounty: pj.Bounty,
		// hunter_audience "verified" limits a program to identity-verified
		// hunters.
		Requirements:  &platform.Requirements{IDVerified: pj.HunterAudience == "verified"},
		ReportCost:    pj.ReportCost,
		Collaboration: pj.Collaboration,
		Country:       strings.ToUpper(strings.TrimSpace(pj.Country)),
		OffersSwag:    pj.Gift,
		Raw:           raw,
	}
	if pj.Disabled || pj.Archived {
		p.State = platform.StateClosed
	}
	if pj.Bounty {
		p.Currency = "EUR" // YesWeHack pays in euros
		if pj.BountyRewardMax > 0 {
			p.BountyMax = &pj.BountyRewardMax
			if pj.BountyRewardMin > 0 {
				p.BountyMin = &pj.BountyRewardMin
			}
		}
	}
	if t, err := time.Parse(time.RFC3339, pj.LastUpdateAt); err == nil {
		t = t.UTC()
		p.ScopeChangedAt = &t
	}
	return p
}

type programDetail struct {
	Bounty bool `json:"bounty"`
	Scopes []struct {
		Scope      string `json:"scope"`
		ScopeType  string `json:"scope_type"`
		AssetValue string `json:"asset_value"`
	} `json:"scopes"`
	OutOfScope    []string    `json:"out_of_scope"`
	Rules         string      `json:"rules"` // Markdown
	Qualifying    []string    `json:"qualifying_vulnerability"`
	NonQualifying []string    `json:"non_qualifying_vulnerability"`
	GridDefault   *rewardGrid `json:"reward_grid_default"`
	GridVeryLow   *rewardGrid `json:"reward_grid_very_low"`
	GridLow       *rewardGrid `json:"reward_grid_low"`
	GridMedium    *rewardGrid `json:"reward_grid_medium"`
	GridHigh      *rewardGrid `json:"reward_grid_high"`
	GridCritical  *rewardGrid `json:"reward_grid_critical"`
}

// rewardGrid is the bounty per severity for assets of one value.
type rewardGrid struct {
	Low      *float64 `json:"bounty_low"`
	Medium   *float64 `json:"bounty_medium"`
	High     *float64 `json:"bounty_high"`
	Critical *float64 `json:"bounty_critical"`
}

// FetchScope implements platform.Platform.
func (c *Client) FetchScope(ctx context.Context, p platform.Program) ([]platform.ScopeEntry, error) {
	entries, _, err := c.FetchScopeDetail(ctx, p)
	return entries, err
}

// FetchScopeDetail implements platform.ScopeDetailFetcher: the program
// document also holds its rules and reward grids.
func (c *Client) FetchScopeDetail(ctx context.Context, p platform.Program) ([]platform.ScopeEntry, platform.ScopeDetail, error) {
	var d programDetail
	if err := c.getJSON(ctx, c.base.String()+"/programs/"+url.PathEscape(p.Handle), &d); err != nil {
		return nil, platform.ScopeDetail{}, fmt.Errorf("yeswehack: program %s: %w", p.Handle, err)
	}
	return d.entries(), platform.ScopeDetail{Policy: d.policy(), Rewards: d.rewards()}, nil
}

// policy is the rules followed by what the program does and doesn't
// accept, which programs edit as often as the rules.
func (d programDetail) policy() string {
	parts := []string{strings.TrimSpace(d.Rules)}
	for _, l := range []struct {
		title string
		items []string
	}{{"Qualifying vulnerabilities", d.Qualifying}, {"Non-qualifying vulnerabilities", d.NonQualifying}} {
		var b strings.Builder
		for _, it := range l.items {
			if it = strings.TrimSpace(it); it != "" {
				b.WriteString("\n* " + it)
			}
		}
		if b.Len() > 0 {
			parts = append(parts, "## "+l.title+"\n"+b.String())
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

// rewards is a row per asset value the program's scope uses, as its page
// shows them, or the default grid when its assets have no value set. A
// program without bounties has none.
func (d programDetail) rewards() []platform.RewardRow {
	out := []platform.RewardRow{}
	if !d.Bounty {
		return out
	}
	used := map[string]bool{}
	for _, s := range d.Scopes {
		used[strings.ToUpper(s.AssetValue)] = true
	}
	for _, t := range []struct {
		value string
		grid  *rewardGrid
	}{{"VERY_LOW", d.GridVeryLow}, {"LOW", d.GridLow}, {"MEDIUM", d.GridMedium}, {"HIGH", d.GridHigh}, {"CRITICAL", d.GridCritical}} {
		if used[t.value] && t.grid.paid() {
			out = append(out, t.grid.row("Asset value: "+strings.ToLower(strings.ReplaceAll(t.value, "_", " "))))
		}
	}
	if len(out) == 0 && d.GridDefault.paid() {
		out = append(out, d.GridDefault.row(""))
	}
	return out
}

func (g *rewardGrid) paid() bool {
	return g != nil && (g.Low != nil || g.Medium != nil || g.High != nil || g.Critical != nil)
}

func (g *rewardGrid) row(scope string) platform.RewardRow {
	amount := func(v *float64) platform.RewardRange {
		if v == nil {
			return platform.RewardRange{}
		}
		return platform.RewardRange{Max: *v}
	}
	return platform.RewardRow{Scope: scope, Low: amount(g.Low), Medium: amount(g.Medium), High: amount(g.High), Critical: amount(g.Critical)}
}

func (d programDetail) entries() []platform.ScopeEntry {
	out := make([]platform.ScopeEntry, 0, len(d.Scopes)+len(d.OutOfScope))
	for _, s := range d.Scopes {
		ident := strings.TrimSpace(s.Scope)
		e := platform.ScopeEntry{
			InScope:           true,
			AssetType:         assetType(s.ScopeType, ident),
			PlatformAssetType: s.ScopeType,
			Identifier:        ident,
			BountyEligible:    d.Bounty,
		}
		if s.AssetValue != "" {
			e.Instruction = "Asset value: " + strings.ToLower(strings.ReplaceAll(s.AssetValue, "_", " "))
		}
		out = append(out, e)
	}
	for _, s := range d.OutOfScope {
		ident := strings.TrimSpace(s)
		if ident == "" {
			continue
		}
		out = append(out, platform.ScopeEntry{
			AssetType:         scope.WebType(ident),
			PlatformAssetType: "out_of_scope",
			Identifier:        ident,
		})
	}
	return out
}

// assetType maps YesWeHack scope types to normalised asset types.
func assetType(t, ident string) string {
	switch t {
	case "web-application", "application":
		return scope.WebType(ident)
	case "wildcard":
		return scope.AssetWildcard
	case "api":
		return scope.AssetAPI
	case "mobile-application-android":
		return scope.AssetAndroid
	case "mobile-application-ios":
		return scope.AssetIOS
	case "ip-address":
		return scope.AssetIP
	case "ai-model":
		return scope.AssetAIModel
	default: // "mobile-application", "other", "challenge", ...
		return scope.AssetOther
	}
}

// getJSON sends an authenticated GET when credentials exist, logging in
// first or again as needed.
func (c *Client) getJSON(ctx context.Context, u string, into any) error {
	return c.doJSON(ctx, http.MethodGet, u, nil, into)
}

// postJSON sends an authenticated POST with a JSON body, logging in first or
// again as needed.
func (c *Client) postJSON(ctx context.Context, u string, body, into any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodPost, u, b, into)
}

func (c *Client) doJSON(ctx context.Context, method, u string, body []byte, into any) error {
	for attempt := 0; ; attempt++ {
		token, err := c.currentToken(ctx)
		if err != nil {
			return err
		}
		var br io.Reader
		if body != nil {
			br = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, br)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := c.http.Do(ctx, req)
		if err != nil {
			return err
		}
		switch res.StatusCode {
		case http.StatusOK:
			if token != "" {
				c.cred.Accepted()
			}
			if err := json.Unmarshal(res.Body, into); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			return nil
		case http.StatusUnauthorized:
			if c.email == "" || attempt > 0 {
				c.cred.Refused("YesWeHack refused the token (401): set a fresh YWH_TOKEN, or YWH_EMAIL and YWH_PASSWORD", false)
				return ErrAuth
			}
			c.dropToken(token) // expired early: log in again once
		case http.StatusForbidden:
			return fmt.Errorf("HTTP 403: %w", platform.ErrScopeUnavailable)
		case http.StatusNotFound:
			return fmt.Errorf("HTTP 404: %w", errNotFound)
		default:
			return fmt.Errorf("HTTP %d", res.StatusCode)
		}
	}
}

// currentToken returns a usable token, logging in if needed. It returns
// "" in public mode.
func (c *Client) currentToken(ctx context.Context) (string, error) {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.host != nil {
		return c.browserToken(ctx, time.Minute)
	}
	fresh := c.token != "" && (c.expires.IsZero() || c.now().Before(c.expires.Add(-time.Minute)))
	if fresh || c.email == "" {
		if c.token != "" && !fresh {
			c.cred.Refused("the pasted YWH_TOKEN has expired: set a fresh one, or YWH_EMAIL and YWH_PASSWORD, and restart", true)
			return "", ErrAuth // a pasted token that has expired
		}
		return c.token, nil
	}
	if c.loginErr != nil {
		return "", c.loginErr
	}
	tok, err := c.login(ctx)
	var le *loginError
	if errors.As(err, &le) {
		c.loginErr = err
	}
	if err != nil {
		if le != nil { // refused, not just unreachable
			c.cred.Refused(le.msg, true)
		}
		return "", err
	}
	c.token, c.expires = tok, tokenExpiry(tok)
	c.cred.SignedIn(&c.expires)
	return tok, nil
}

// renewBefore is how long before its token expires KeepAlive signs in
// again: more than the scheduler's keep-alive interval, so polls never
// find it expired.
const renewBefore = 15 * time.Minute

// KeepAlive implements platform.KeepAliver. With a login it keeps a token
// ready: it signs in when there's none, or when the one held expires
// within renewBefore. A pasted token can't be renewed.
func (c *Client) KeepAlive(ctx context.Context) error {
	if c.email == "" {
		return nil
	}
	if c.host != nil {
		c.authMu.Lock()
		defer c.authMu.Unlock()
		_, err := c.browserToken(ctx, renewBefore)
		return err
	}
	c.authMu.Lock()
	if c.token != "" && !c.expires.IsZero() && c.now().After(c.expires.Add(-renewBefore)) {
		c.token = ""
	}
	c.authMu.Unlock()
	_, err := c.currentToken(ctx)
	return err
}

func (c *Client) dropToken(tok string) {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.token == tok {
		c.token = ""
	}
}

// loginError is a rejection of the user's credentials. It sticks until
// restart so a wrong password isn't retried every poll.
type loginError struct{ msg string }

func (e *loginError) Error() string {
	return "yeswehack: login rejected: " + e.msg + " (fix the YWH_ settings and restart HuntDeck)"
}

// login posts the email and password, then the TOTP code if 2FA is on.
func (c *Client) login(ctx context.Context) (string, error) {
	var first struct {
		Token     string `json:"token"`
		TOTPToken string `json:"totp_token"`
	}
	status, err := c.post(ctx, "/login", map[string]string{"email": c.email, "password": c.password}, &first)
	switch {
	case err != nil:
		return "", fmt.Errorf("yeswehack: login: %w", err)
	case status == http.StatusBadRequest || status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "", &loginError{fmt.Sprintf("email or password not accepted (HTTP %d)", status)}
	case status != http.StatusOK:
		return "", fmt.Errorf("yeswehack: login: HTTP %d", status)
	case first.Token != "":
		return first.Token, nil
	case first.TOTPToken == "":
		return "", errors.New("yeswehack: login: reply has neither token nor totp_token (login changed?)")
	case c.totpSecret == "":
		return "", &loginError{"2FA is on; set YWH_TOTP_SECRET"}
	}

	// Allow one retry in the next 30-second window, for clock skew.
	for try := 0; try < 2; try++ {
		code, err := c.totpCode(ctx)
		if err != nil {
			return "", err
		}
		var second struct {
			Token string `json:"token"`
		}
		status, err := c.post(ctx, "/account/totp", map[string]string{"token": first.TOTPToken, "code": code}, &second)
		switch {
		case err != nil:
			return "", fmt.Errorf("yeswehack: login: totp: %w", err)
		case status == http.StatusOK && second.Token != "":
			return second.Token, nil
		case status == http.StatusOK:
			return "", errors.New("yeswehack: login: totp reply has no token")
		case status != http.StatusBadRequest:
			return "", fmt.Errorf("yeswehack: login: totp: HTTP %d", status)
		}
	}
	return "", &loginError{"authenticator code not accepted; check YWH_TOTP_SECRET and the server clock"}
}

func (c *Client) post(ctx context.Context, path string, body any, into any) (int, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base.String()+path, bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(ctx, req)
	if err != nil {
		return 0, err
	}
	if res.StatusCode == http.StatusOK {
		if err := json.Unmarshal(res.Body, into); err != nil {
			return 0, fmt.Errorf("decode response: %w", err)
		}
	}
	return res.StatusCode, nil
}

// totpCode returns a code, waiting for the next 30-second step if this one
// was already used.
func (c *Client) totpCode(ctx context.Context) (string, error) {
	now := c.now()
	if totp.Step(now) == c.lastTOTPStep {
		if err := c.sleep(ctx, totp.NextStep(now).Sub(now)+time.Second); err != nil {
			return "", err
		}
		now = c.now()
	}
	code, err := totp.Code(c.totpSecret, now)
	if err != nil {
		return "", &loginError{"YWH_TOTP_SECRET: " + err.Error()}
	}
	c.lastTOTPStep = totp.Step(now)
	return code, nil
}

// tokenExpiry reads the exp claim of a JWT without verifying it (we only
// use it to log in again before the server starts refusing the token).
func tokenExpiry(tok string) time.Time {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp <= 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
