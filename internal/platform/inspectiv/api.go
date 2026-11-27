package inspectiv

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// errorEnvelope is Inspectiv DRF's 4xx/5xx error payload.
type errorEnvelope struct {
	Error      string `json:"error"`
	Detail     string `json:"detail"`
	StatusCode int    `json:"status_code"`
}

// apiError represents a non-2xx HTTP response from the Inspectiv API.
type apiError struct {
	StatusCode int
	Message    string
}

func (e *apiError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("inspectiv: HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("inspectiv: HTTP %d", e.StatusCode)
}

// getJSON performs an authenticated GET request against Inspectiv's API,
// renewing the session once via browser login on HTTP 401 when configured.
func (c *Client) getJSON(ctx context.Context, path string, into any) error {
	return c.doGET(ctx, path, into, true)
}

// getJSONNoRenew performs an authenticated GET without triggering browser
// re-login (used by verifySession).
func (c *Client) getJSONNoRenew(ctx context.Context, path string, into any) error {
	return c.doGET(ctx, path, into, false)
}

func (c *Client) doGET(ctx context.Context, path string, into any, allowRenew bool) error {
	u := strings.TrimSuffix(c.base.String(), "/") + path
	renewed := false

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		if tok := c.currentToken(); tok != "" {
			req.Header.Set("Authorization", "Token "+tok)
		}

		res, err := c.http.Do(ctx, req)
		if err != nil {
			return err
		}

		if res.StatusCode == http.StatusUnauthorized {
			msg := parseErrorMessage(res.Body)
			if allowRenew && !renewed && c.canBrowserLogin() {
				renewed = true
				if rerr := c.renewSession(ctx); rerr != nil {
					return rerr
				}
				continue
			}
			if c.authenticated() && !c.canBrowserLogin() {
				c.cred.Refused("Inspectiv rejected the API token (401): set a fresh INS_TOKEN", true)
			}
			if msg != "" {
				return fmt.Errorf("%w: %s", ErrSessionExpired, msg)
			}
			return ErrSessionExpired
		}

		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return &apiError{
				StatusCode: res.StatusCode,
				Message:    parseErrorMessage(res.Body),
			}
		}

		if into == nil || len(res.Body) == 0 {
			return nil
		}
		if err := json.Unmarshal(res.Body, into); err != nil {
			return fmt.Errorf("inspectiv: decode %s: %w", path, err)
		}
		return nil
	}
}

func parseErrorMessage(body []byte) string {
	var env errorEnvelope
	if len(body) > 0 && json.Unmarshal(body, &env) == nil {
		if msg := strings.TrimSpace(env.Error); msg != "" {
			return msg
		}
		if msg := strings.TrimSpace(env.Detail); msg != "" {
			return msg
		}
	}
	return ""
}
