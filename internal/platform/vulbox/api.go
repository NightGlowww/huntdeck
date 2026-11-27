package vulbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
)

const (
	appRateMaxAttempts = 4
	appRateBaseWait    = 2 * time.Second
	appRateMaxWait     = 30 * time.Second
)

// envelope is the standard vapi.vulbox.com JSON response wrapper.
// Nearly all endpoints return HTTP 200 and encode status in Code.
type envelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// apiError represents a non-200 application code inside a VulBox JSON envelope.
type apiError struct {
	Code int
	Msg  string
}

func (e *apiError) Error() string {
	if e.Msg != "" {
		return fmt.Sprintf("vulbox API code %d: %s", e.Code, e.Msg)
	}
	return fmt.Sprintf("vulbox API code %d", e.Code)
}

func isAPICode(err error, codes ...int) bool {
	var ae *apiError
	if !errors.As(err, &ae) {
		return false
	}
	for _, code := range codes {
		if ae.Code == code {
			return true
		}
	}
	return false
}

func isAuthCode(code int) bool {
	return code == 401 || code == 402 || code == 401004
}

func isRateLimitCode(code int) bool {
	return code == 429 || code == 449 || code == 400004
}

// getJSON performs a GET request against vapi.vulbox.com, inspecting the JSON
// envelope code. It retries app-level rate limits (429/449/400004) with
// bounded backoff and renews the session once on 401/402/401004 if browser
// credentials are configured.
func (c *Client) getJSON(ctx context.Context, path string, extraHeaders map[string]string, into any) error {
	return c.doJSON(ctx, http.MethodGet, path, nil, extraHeaders, into, true)
}

// getJSONNoRenew is getJSON without triggering browser re-login (used by sessionValid).
func (c *Client) getJSONNoRenew(ctx context.Context, path string, extraHeaders map[string]string, into any) error {
	return c.doJSON(ctx, http.MethodGet, path, nil, extraHeaders, into, false)
}

// postJSON performs a state-changing POST using the non-retrying action client
// (MaxAttempts: 1). It never replays POSTs on network errors or rate limits.
func (c *Client) postJSON(ctx context.Context, path string, body any, extraHeaders map[string]string, into any) error {
	return c.doJSON(ctx, http.MethodPost, path, body, extraHeaders, into, true)
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, extraHeaders map[string]string, into any, allowRenew bool) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("vulbox: encode body: %w", err)
		}
	}

	u := c.base.String() + path
	hc := c.http
	maxAppAttempts := appRateMaxAttempts
	if method == http.MethodPost {
		hc = c.action
		maxAppAttempts = 1
	}

	renewed := false
	for attempt := 1; ; attempt++ {
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
		c.attachHeaders(req, payload != nil, extraHeaders)

		res, err := hc.Do(ctx, req)
		if err != nil {
			return err
		}

		env, parseErr := decodeEnvelope(res)
		if parseErr != nil {
			if res.StatusCode == http.StatusUnauthorized {
				return c.handleAuthFailure(ctx, allowRenew, &renewed)
			}
			return parseErr
		}

		switch {
		case env.Code == 200:
			return decodeData(env.Data, into)

		case isAuthCode(env.Code):
			if allowRenew && !renewed && c.canBrowserLogin() {
				renewed = true
				if err := c.renewSession(ctx); err != nil {
					return err
				}
				attempt = 0
				continue
			}
			if c.authenticated() && !c.canBrowserLogin() {
				c.cred.Refused("VulBox session expired or invalid: paste a fresh VB_SESSION", true)
			}
			return fmt.Errorf("%w (code %d)", ErrSessionExpired, env.Code)

		case env.Code == 403004:
			return ErrMFARequired

		case env.Code == 403005:
			return ErrInitPassword

		case isRateLimitCode(env.Code):
			c.appRateLimited.Add(1)
			ae := &apiError{Code: env.Code, Msg: env.Msg}
			if attempt >= maxAppAttempts {
				return fmt.Errorf("vulbox: rate limited after %d attempts: %w", attempt, ae)
			}
			wait := appBackoff(attempt)
			if err := c.sleep(ctx, wait); err != nil {
				return err
			}
			continue

		default:
			return &apiError{Code: env.Code, Msg: env.Msg}
		}
	}
}

func (c *Client) handleAuthFailure(ctx context.Context, allowRenew bool, renewed *bool) error {
	if allowRenew && !*renewed && c.canBrowserLogin() {
		*renewed = true
		if err := c.renewSession(ctx); err != nil {
			return err
		}
	}
	if c.authenticated() && !c.canBrowserLogin() {
		c.cred.Refused("VulBox session expired or invalid (HTTP 401): paste a fresh VB_SESSION", true)
	}
	return ErrSessionExpired
}

func (c *Client) attachHeaders(req *http.Request, hasBody bool, extra map[string]string) {
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Client-Type", "web")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", c.user.String())
	req.Header.Set("Referer", c.user.String()+"/")
	if id := c.currentUUID(); id != "" {
		req.Header.Set("uuid", id)
	}
	if !c.publicOnly {
		if tok := c.currentToken(); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	if hasBody {
		req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
}

func decodeEnvelope(res *httpclient.Response) (envelope, error) {
	if res.StatusCode != http.StatusOK {
		// Some error responses still include a JSON envelope; try decoding first.
		var env envelope
		if len(res.Body) > 0 && json.Unmarshal(res.Body, &env) == nil && env.Code != 0 {
			return env, nil
		}
		return envelope{}, fmt.Errorf("vulbox: HTTP %d", res.StatusCode)
	}
	var env envelope
	if err := json.Unmarshal(res.Body, &env); err != nil {
		return envelope{}, fmt.Errorf("vulbox: decode envelope: %w", err)
	}
	return env, nil
}

func decodeData(raw json.RawMessage, into any) error {
	if into == nil {
		return nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	// VulBox sometimes returns [] when an object is empty, or {} when an array is empty.
	if bytes.Equal(trimmed, []byte("[]")) || bytes.Equal(trimmed, []byte("{}")) {
		if err := json.Unmarshal(trimmed, into); err == nil {
			return nil
		}
		return nil
	}
	if err := json.Unmarshal(trimmed, into); err != nil {
		return fmt.Errorf("vulbox: decode data: %w", err)
	}
	return nil
}

func appBackoff(attempt int) time.Duration {
	d := appRateBaseWait << min(attempt-1, 5)
	if d > appRateMaxWait {
		d = appRateMaxWait
	}
	jitter := 0.8 + 0.4*rand.Float64() //nolint:gosec // backoff jitter
	return time.Duration(float64(d) * jitter)
}

// --- Flexible JSON types for VulBox's inconsistent wire types ---

// flexBool decodes JSON booleans (true/false), numbers (1/0), or strings ("1"/"true").
type flexBool bool

func (b flexBool) Bool() bool { return bool(b) }

func (b *flexBool) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "" || s == "null" {
		*b = false
		return nil
	}
	var bv bool
	if err := json.Unmarshal(data, &bv); err == nil {
		*b = flexBool(bv)
		return nil
	}
	var iv int64
	if err := json.Unmarshal(data, &iv); err == nil {
		*b = flexBool(iv != 0)
		return nil
	}
	var sv string
	if err := json.Unmarshal(data, &sv); err == nil {
		sv = strings.ToLower(strings.TrimSpace(sv))
		*b = flexBool(sv == "1" || sv == "true" || sv == "yes")
		return nil
	}
	return fmt.Errorf("vulbox: cannot decode flexBool %s", s)
}

// flexFloat decodes JSON numbers or numeric strings (e.g. "1200.00" or 1200).
type flexFloat float64

func (f flexFloat) Float64() float64 { return float64(f) }

func (f *flexFloat) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "" || s == "null" || s == `""` {
		*f = 0
		return nil
	}
	var fv float64
	if err := json.Unmarshal(data, &fv); err == nil {
		*f = flexFloat(fv)
		return nil
	}
	var sv string
	if err := json.Unmarshal(data, &sv); err == nil {
		sv = strings.TrimSpace(strings.ReplaceAll(sv, ",", ""))
		if sv == "" || sv == "--" {
			*f = 0
			return nil
		}
		v, err := strconv.ParseFloat(sv, 64)
		if err != nil {
			*f = 0
			return nil
		}
		*f = flexFloat(v)
		return nil
	}
	return fmt.Errorf("vulbox: cannot decode flexFloat %s", s)
}

// flexInt64 decodes JSON integers, floats, or numeric strings.
type flexInt64 int64

func (n flexInt64) Int64() int64 { return int64(n) }

func (n *flexInt64) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "" || s == "null" || s == `""` {
		*n = 0
		return nil
	}
	var iv int64
	if err := json.Unmarshal(data, &iv); err == nil {
		*n = flexInt64(iv)
		return nil
	}
	var fv float64
	if err := json.Unmarshal(data, &fv); err == nil {
		*n = flexInt64(int64(fv))
		return nil
	}
	var sv string
	if err := json.Unmarshal(data, &sv); err == nil {
		sv = strings.TrimSpace(sv)
		if sv == "" {
			*n = 0
			return nil
		}
		v, err := strconv.ParseInt(sv, 10, 64)
		if err != nil {
			*n = 0
			return nil
		}
		*n = flexInt64(v)
		return nil
	}
	return fmt.Errorf("vulbox: cannot decode flexInt64 %s", s)
}

// flexString decodes JSON strings, numbers, or null (ignoring objects/arrays).
type flexString string

func (s flexString) String() string { return string(s) }

func (s *flexString) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*s = ""
		return nil
	}
	var sv string
	if err := json.Unmarshal(data, &sv); err == nil {
		*s = flexString(sv)
		return nil
	}
	var nv json.Number
	if err := json.Unmarshal(data, &nv); err == nil {
		*s = flexString(nv.String())
		return nil
	}
	// VulBox occasionally returns an object/array where another endpoint sends a string
	// (e.g. task_skill or range_annex); treat gracefully as empty string.
	*s = ""
	return nil
}

// cstZone is Asia/Shanghai (UTC+8), which VulBox uses for unzoned timestamps.
var cstZone = time.FixedZone("Asia/Shanghai", 8*3600)

// parseCSTTime parses VulBox timestamps (Unix epoch seconds, RFC3339, or
// Asia/Shanghai formatted strings) and returns UTC. Returns nil when unset.
func parseCSTTime(raw json.RawMessage) *time.Time {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" || s == "0" || s == `""` {
		return nil
	}
	// Numeric Unix epoch seconds.
	if iv, err := strconv.ParseInt(s, 10, 64); err == nil {
		if iv <= 0 {
			return nil
		}
		t := time.Unix(iv, 0).UTC()
		return &t
	}
	var sv string
	if err := json.Unmarshal(raw, &sv); err != nil {
		return nil
	}
	return parseCSTString(sv)
}

// parseCSTString parses a VulBox date/time string in Asia/Shanghai into UTC.
func parseCSTString(sv string) *time.Time {
	sv = strings.TrimSpace(sv)
	if sv == "" || strings.HasPrefix(sv, "0000-00-00") || strings.HasPrefix(sv, "1970-01-01") {
		return nil
	}
	if iv, err := strconv.ParseInt(sv, 10, 64); err == nil {
		if iv <= 0 {
			return nil
		}
		t := time.Unix(iv, 0).UTC()
		return &t
	}
	if t, err := time.Parse(time.RFC3339, sv); err == nil {
		t = t.UTC()
		return &t
	}
	for _, layout := range []string{
		"2006/01/02 15:04:05",
		"2006-01-02 15:04:05",
		"2006/01/02 15:04",
		"2006-01-02 15:04",
		"2006-01-02",
		"2006/01/02",
	} {
		if t, err := time.ParseInLocation(layout, sv, cstZone); err == nil {
			t = t.UTC()
			return &t
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
