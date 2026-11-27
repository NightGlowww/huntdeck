package vulbox

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// parseSession accepts:
//   - "Bearer <jwt>"
//   - "Cookie: a=1; b=2" or "a=1; b=2"
//   - a bare vulbox_token value (no "=")
func parseSession(raw string) []*http.Cookie {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	if len(s) > 7 && strings.EqualFold(s[:7], "Bearer ") {
		tok := strings.TrimSpace(s[7:])
		if tok == "" {
			return nil
		}
		return []*http.Cookie{{Name: tokenCookie, Value: tok, Path: "/"}} //nolint:gosec // client-side jar seed
	}
	if len(s) > 7 && strings.EqualFold(s[:7], "Cookie:") {
		s = strings.TrimSpace(s[7:])
	}
	if !strings.Contains(s, "=") {
		return []*http.Cookie{{Name: tokenCookie, Value: s, Path: "/"}} //nolint:gosec // client-side jar seed
	}
	cs, err := http.ParseCookie(s)
	if err != nil {
		return nil
	}
	for _, ck := range cs {
		ck.Path = "/"
	}
	return cs
}

// parseJWTExpiry decodes the payload segment of a JWT and returns its exp
// claim as a UTC time, or nil if the string is not a JWT with a numeric exp.
// Never logs token contents.
func parseJWTExpiry(tok string) *time.Time {
	parts := strings.Split(strings.TrimSpace(tok), ".")
	if len(parts) != 3 || parts[1] == "" {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims struct {
		Exp flexFloat `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	sec := int64(claims.Exp.Float64())
	if sec <= 0 {
		return nil
	}
	t := time.Unix(sec, 0).UTC()
	return &t
}

func newClientUUID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
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
