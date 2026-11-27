package gobugfree

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
)

// apiError represents GObugfree's standard JSON error envelope.
type apiError struct {
	Status  int    `json:"status"`
	Title   string `json:"title"`
	Message string `json:"message"`
}

func (e *apiError) Error() string {
	msg := firstNonEmpty(e.Message, e.Title)
	if msg != "" {
		return fmt.Sprintf("gobugfree: HTTP %d: %s", e.Status, msg)
	}
	return fmt.Sprintf("gobugfree: HTTP %d", e.Status)
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
	TokenType        string `json:"token_type"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (c *Client) getJSON(ctx context.Context, path string, into any) error {
	return c.doJSON(ctx, http.MethodGet, path, nil, into, true)
}

func (c *Client) getJSONNoRenew(ctx context.Context, path string, into any) error {
	return c.doJSON(ctx, http.MethodGet, path, nil, into, false)
}

func (c *Client) postJSON(ctx context.Context, path string, body any, into any) error {
	return c.doJSON(ctx, http.MethodPost, path, body, into, true)
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, into any, allowRenew bool) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("gobugfree: encode body: %w", err)
		}
	}

	u := c.base.String() + path
	hc := c.http
	if method == http.MethodPost {
		hc = c.action
	}

	renewed := false
	for {
		var req *http.Request
		var err error
		if payload != nil {
			req, err = http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
		} else {
			req, err = http.NewRequestWithContext(ctx, method, u, nil)
		}
		if err != nil {
			return err
		}
		c.attachHeaders(req, payload != nil)

		res, err := hc.Do(ctx, req)
		if err != nil {
			return err
		}

		if isAuthFailureResponse(res) {
			if allowRenew && !renewed && c.authenticated() {
				renewed = true
				if rerr := c.renewSession(ctx); rerr != nil {
					return rerr
				}
				continue
			}
			if c.authenticated() && !c.canBrowserLogin() {
				c.cred.Refused("GObugfree session expired or invalid: set a fresh GBF_TOKEN", true)
			}
			return ErrSessionExpired
		}

		if res.StatusCode == http.StatusNotFound {
			return errNotFound
		}
		if res.StatusCode == http.StatusForbidden {
			return decodeStatusError(res)
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return decodeStatusError(res)
		}

		if into == nil || len(bytes.TrimSpace(res.Body)) == 0 {
			if c.authenticated() {
				c.cred.Accepted()
			}
			return nil
		}
		if err := json.Unmarshal(res.Body, into); err != nil {
			return fmt.Errorf("gobugfree: decode JSON from %s: %w", path, err)
		}
		if c.authenticated() {
			c.cred.Accepted()
		}
		return nil
	}
}

func (c *Client) attachHeaders(req *http.Request, hasBody bool) {
	req.Header.Set("Accept", "application/json")
	if !c.publicOnly {
		if tok := c.currentAccessToken(); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
}

// isAuthFailureResponse detects 401, redirects to /auth/login or id.gobugfree.com,
// and HTML responses returned where JSON was requested (Keycloak proxy redirect).
func isAuthFailureResponse(res *httpclient.Response) bool {
	if res.StatusCode == http.StatusUnauthorized {
		return true
	}
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		return true
	}
	if res.StatusCode == http.StatusOK {
		ct := strings.ToLower(res.Header.Get("Content-Type"))
		trimmed := bytes.TrimSpace(res.Body)
		if strings.Contains(ct, "text/html") || bytes.HasPrefix(trimmed, []byte("<!")) || bytes.HasPrefix(bytes.ToLower(trimmed), []byte("<html")) {
			return true
		}
	}
	return false
}

func decodeStatusError(res *httpclient.Response) error {
	var ae apiError
	if len(res.Body) > 0 && json.Unmarshal(res.Body, &ae) == nil && (ae.Message != "" || ae.Title != "" || ae.Status != 0) {
		if ae.Status == 0 {
			ae.Status = res.StatusCode
		}
		return &ae
	}
	return fmt.Errorf("gobugfree: HTTP %d", res.StatusCode)
}

// refreshAccessTokenLocked exchanges refresh_token for a new access_token via
// Keycloak's token endpoint. Caller must hold c.authMu.
func (c *Client) refreshAccessTokenLocked(ctx context.Context) error {
	_, refresh := c.tokens()
	if refresh == "" {
		return ErrSessionExpired
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {defaultClientID},
		"refresh_token": {refresh},
	}
	tokResp, status, err := c.postTokenForm(ctx, form)
	if err != nil {
		if status == http.StatusBadRequest || status == http.StatusUnauthorized {
			c.setTokens("", "")
			if !c.canBrowserLogin() {
				c.cred.Refused("GObugfree refresh token expired or revoked: set a fresh GBF_TOKEN", true)
			}
			return ErrSessionExpired
		}
		return err
	}
	newRefresh := firstNonEmpty(tokResp.RefreshToken, refresh)
	c.setTokens(tokResp.AccessToken, newRefresh)
	exp := c.tokenExpiry(tokResp)
	c.cred.SignedIn(exp)
	if c.logins != nil {
		c.logins.Renewed()
	}
	c.log.Info("gobugfree: refreshed access token",
		"expires_in", tokResp.ExpiresIn,
		"has_refresh", newRefresh != "",
	)
	return nil
}

func (c *Client) postTokenForm(ctx context.Context, form url.Values) (tokenResponse, int, error) {
	endpoint := c.id.String() + "/protocol/openid-connect/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	res, err := c.action.Do(ctx, req)
	if err != nil {
		return tokenResponse{}, 0, err
	}
	var tr tokenResponse
	if len(res.Body) > 0 {
		_ = json.Unmarshal(res.Body, &tr)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		msg := firstNonEmpty(tr.ErrorDescription, tr.Error)
		if msg != "" {
			return tr, res.StatusCode, fmt.Errorf("gobugfree: token endpoint HTTP %d: %s", res.StatusCode, msg)
		}
		return tr, res.StatusCode, fmt.Errorf("gobugfree: token endpoint HTTP %d", res.StatusCode)
	}
	if tr.AccessToken == "" {
		return tr, res.StatusCode, fmt.Errorf("gobugfree: token endpoint returned empty access_token")
	}
	return tr, res.StatusCode, nil
}

func (c *Client) tokenExpiry(tr tokenResponse) *time.Time {
	if tr.RefreshToken != "" {
		if exp := parseJWTExpiry(tr.RefreshToken); exp != nil {
			return exp
		}
		if tr.RefreshExpiresIn > 0 {
			t := c.now().Add(time.Duration(tr.RefreshExpiresIn) * time.Second).UTC()
			return &t
		}
	}
	if exp := parseJWTExpiry(tr.AccessToken); exp != nil {
		return exp
	}
	if tr.ExpiresIn > 0 {
		t := c.now().Add(time.Duration(tr.ExpiresIn) * time.Second).UTC()
		return &t
	}
	return nil
}
