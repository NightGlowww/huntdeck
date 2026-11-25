package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
)

// cloudflare calls Cloudflare's API with the user's token. The token is
// only ever in the Authorization header, never in an error.
type cloudflare struct {
	base  string // https://api.cloudflare.com/client/v4
	token string
	http  *httpclient.Client
}

// CFError is Cloudflare's answer to a call that failed: its status and the
// errors it listed, plus what the call needs of the token.
type CFError struct {
	Call   string // what the call was for, e.g. "create the database"
	Needs  string // the token permission it needs, e.g. "D1 Edit"
	Status int
	Codes  []int
	Text   string // Cloudflare's own messages
}

func (e *CFError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Cloudflare couldn't %s", e.Call)
	switch {
	case e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden || e.has(refusedCodes...):
		if e.Needs != "" {
			fmt.Fprintf(&b, ": the token was refused, or it lacks the %s permission", e.Needs)
		} else {
			b.WriteString(": the token was refused")
		}
	case e.Text != "":
		b.WriteString(": " + e.Text)
	default:
		fmt.Fprintf(&b, " (HTTP %d)", e.Status)
	}
	return b.String()
}

// refusedCodes are Cloudflare's for a token it won't take: malformed (6003,
// 6111), invalid (1000, 9103, 9106, 9109) or without the permission (10000).
var refusedCodes = []int{1000, 6003, 6111, 9103, 9106, 9109, 10000}

func (e *CFError) has(codes ...int) bool {
	for _, c := range e.Codes {
		for _, w := range codes {
			if c == w {
				return true
			}
		}
	}
	return false
}

// notFound reports whether Cloudflare said the thing doesn't exist.
func notFound(err error) bool {
	var cf *CFError
	return errors.As(err, &cf) && (cf.Status == http.StatusNotFound || cf.has(10007, 7003))
}

func newCloudflare(base, token string, hc *httpclient.Client) *cloudflare {
	if hc == nil {
		hc = httpclient.New(httpclient.Options{RatePerSecond: 4, Burst: 4, MaxAttempts: 3, Timeout: 60 * time.Second,
			RateLimitWait: 5 * time.Second, MaxBackoff: 30 * time.Second, MaxBodyBytes: 8 << 20})
	}
	return &cloudflare{base: strings.TrimRight(base, "/"), token: token, http: hc}
}

// envelope is every Cloudflare API answer.
type envelope struct {
	Success bool            `json:"success"`
	Errors  []cfMessage     `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type cfMessage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// call sends a JSON body (nil for none) and decodes the result into out
// (nil to ignore it). call and needs name it in an error.
func (c *cloudflare) call(ctx context.Context, method, path string, body, out any, what, needs string) error {
	var rd *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := c.request(ctx, method, path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req, out, what, needs)
}

// upload sends a multipart body.
func (c *cloudflare) upload(ctx context.Context, path string, body []byte, contentType string, out any, what, needs string) error {
	req, err := c.request(ctx, http.MethodPut, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	return c.do(req, out, what, needs)
}

func (c *cloudflare) request(ctx context.Context, method, path string, body *bytes.Reader) (*http.Request, error) {
	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequestWithContext(ctx, method, c.base+path, body)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, c.base+path, nil)
	}
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *cloudflare) do(req *http.Request, out any, what, needs string) error {
	res, err := c.http.Do(req.Context(), req)
	if err != nil {
		return fmt.Errorf("cloudflare: %s: %w", what, err)
	}
	var env envelope
	jsonErr := json.Unmarshal(res.Body, &env)
	if res.StatusCode >= 300 || jsonErr != nil || !env.Success {
		e := &CFError{Call: what, Needs: needs, Status: res.StatusCode}
		var texts []string
		for _, m := range env.Errors {
			e.Codes = append(e.Codes, m.Code)
			if m.Message != "" {
				texts = append(texts, clip(m.Message, 300))
			}
		}
		e.Text = strings.Join(texts, "; ")
		return e
	}
	if out == nil || len(env.Result) == 0 || string(env.Result) == "null" {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("cloudflare: %s: unexpected answer: %w", what, err)
	}
	return nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}
