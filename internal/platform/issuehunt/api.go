package issuehunt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// httpError represents a non-2xx HTTP response from IssueHunt, whose error
// bodies are often plain text rather than JSON.
type httpError struct {
	StatusCode int
	Path       string
	Body       string
}

func (e *httpError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("issuehunt: %s HTTP %d: %s", e.Path, e.StatusCode, e.Body)
	}
	return fmt.Sprintf("issuehunt: %s HTTP %d", e.Path, e.StatusCode)
}

func isHTTPStatus(err error, codes ...int) bool {
	var he *httpError
	if !errors.As(err, &he) {
		return false
	}
	for _, code := range codes {
		if he.StatusCode == code {
			return true
		}
	}
	return false
}

func (c *Client) getJSON(ctx context.Context, path string, into any) error {
	u := strings.TrimSuffix(c.base.String(), "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("issuehunt: build request %s: %w", path, err)
	}
	c.attachHeaders(req)

	res, err := c.http.Do(ctx, req)
	if err != nil {
		return fmt.Errorf("issuehunt: %w", err)
	}

	if res.StatusCode == http.StatusUnauthorized {
		msg := sanitizeErrBody(res.Body)
		if c.authenticated() {
			c.cred.Refused("IssueHunt rejected the API key (HTTP 401): check IH_API_KEY", true)
		}
		if msg != "" {
			return fmt.Errorf("%w: %s", ErrAuth, msg)
		}
		return ErrAuth
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &httpError{
			StatusCode: res.StatusCode,
			Path:       path,
			Body:       sanitizeErrBody(res.Body),
		}
	}

	if into == nil {
		return nil
	}
	if err := json.Unmarshal(res.Body, into); err != nil {
		return fmt.Errorf("issuehunt: decode %s: %w", path, err)
	}
	return nil
}

func (c *Client) attachHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	if c.authenticated() {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

func sanitizeErrBody(body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return ""
	}
	s = strings.Join(strings.Fields(s), " ")
	const maxLen = 200
	if len(s) > maxLen {
		s = s[:maxLen] + "..."
	}
	return s
}

// parseTime parses both RFC 3339 (e.g. "2024-08-09T03:59:43.000Z") and
// JS Date.prototype.toString() (e.g. "Fri Nov 27 2026 10:40:36 GMT+0000 (Coordinated Universal Time)")
// timestamps, returning UTC.
func parseTime(raw string) *time.Time {
	s := strings.TrimSpace(raw)
	if s == "" || s == "null" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		u := t.UTC()
		return &u
	}
	// JS Date.toString() typically appends " (Coordinated Universal Time)" or similar.
	if idx := strings.Index(s, " ("); idx > 0 && strings.HasSuffix(s, ")") {
		s = strings.TrimSpace(s[:idx])
	}
	for _, layout := range []string{
		"Mon Jan 02 2006 15:04:05 GMT-0700",
		"Mon Jan 2 2006 15:04:05 GMT-0700",
		time.RFC1123Z,
		time.RFC1123,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			u := t.UTC()
			return &u
		}
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
