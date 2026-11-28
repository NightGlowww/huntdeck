package browser

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Page is one tab.
type Page struct {
	b       *Browser
	session string
	target  string  // the tab's ID
	x, y    float64 // where the mouse is
}

// NewPage opens a blank tab.
func (b *Browser) NewPage(ctx context.Context) (*Page, error) {
	res, err := b.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"})
	if err != nil {
		return nil, err
	}
	var t struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(res, &t); err != nil {
		return nil, err
	}
	return b.AttachPage(ctx, t.TargetID)
}

// Tab is one of Chrome's open tabs.
type Tab struct {
	ID  string `json:"targetId"`
	URL string `json:"url"`
}

// Tabs lists the open tabs.
func (b *Browser) Tabs(ctx context.Context) ([]Tab, error) {
	res, err := b.call(ctx, "", "Target.getTargets", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		TargetInfos []struct {
			Tab
			Type string `json:"type"`
		} `json:"targetInfos"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	var out []Tab
	for _, t := range r.TargetInfos {
		if t.Type == "page" {
			out = append(out, t.Tab)
		}
	}
	return out, nil
}

// AttachPage drives an open tab (see Tabs).
func (b *Browser) AttachPage(ctx context.Context, id string) (*Page, error) {
	res, err := b.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": id, "flatten": true})
	if err != nil {
		return nil, err
	}
	var s struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(res, &s); err != nil {
		return nil, err
	}
	return &Page{b: b, session: s.SessionID, target: id}, nil
}

// ID is the tab's, as in Tabs.
func (p *Page) ID() string { return p.target }

// Activate brings the tab to the front of its window.
func (p *Page) Activate(ctx context.Context) error {
	_, err := p.b.call(ctx, "", "Target.activateTarget", map[string]any{"targetId": p.target})
	return err
}

// Navigate loads u. It returns once navigation has started; use WaitFor
// for what the page should show. A network change mid-load (Chrome's
// ERR_NETWORK_CHANGED, as when a VPN interface comes up) is retried.
func (p *Page) Navigate(ctx context.Context, u string) error {
	for attempt := 1; ; attempt++ {
		res, err := p.b.call(ctx, p.session, "Page.navigate", map[string]any{"url": u})
		if err != nil {
			return err
		}
		var r struct {
			ErrorText string `json:"errorText"`
		}
		if json.Unmarshal(res, &r) != nil || r.ErrorText == "" {
			return nil
		}
		if r.ErrorText != "net::ERR_NETWORK_CHANGED" || attempt == 4 {
			return fmt.Errorf("browser: navigate: %s", r.ErrorText)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// Eval runs a JavaScript expression in the page and decodes its value
// (awaiting it if it is a promise) into out, if out isn't nil.
func (p *Page) Eval(ctx context.Context, expr string, out any) error {
	res, err := p.b.call(ctx, p.session, "Runtime.evaluate", map[string]any{
		"expression": expr, "returnByValue": true, "awaitPromise": true,
	})
	if err != nil {
		return err
	}
	var r struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return err
	}
	if r.ExceptionDetails != nil {
		return fmt.Errorf("browser: script error: %s", r.ExceptionDetails.Text)
	}
	if out == nil || len(r.Result.Value) == 0 {
		return nil
	}
	return json.Unmarshal(r.Result.Value, out)
}

// WaitFor polls a JavaScript expression until it returns a non-empty
// string, which it returns, or until timeout. Script errors (such as during
// a navigation) count as not yet.
func (p *Page) WaitFor(ctx context.Context, expr string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		var s string
		if err := p.Eval(ctx, "(() => { try { return String(("+expr+") || '') } catch (e) { return '' } })()", &s); err == nil && s != "" {
			return s, nil
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("browser: timed out waiting for the page: %w", ctx.Err())
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// Focus focuses the element matching a CSS selector.
func (p *Page) Focus(ctx context.Context, selector string) error {
	var ok bool
	sel, _ := json.Marshal(selector)
	if err := p.Eval(ctx, "(() => { const e = document.querySelector("+string(sel)+"); if (!e) return false; e.focus(); return true })()", &ok); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("browser: no element matches %s", selector)
	}
	return nil
}

// Cookies returns every cookie the browser holds, with Domain set to the
// cookie's host (no leading dot for host-only cookies).
func (b *Browser) Cookies(ctx context.Context) ([]*http.Cookie, error) {
	res, err := b.call(ctx, "", "Storage.getCookies", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Cookies []struct {
			Name     string  `json:"name"`
			Value    string  `json:"value"`
			Domain   string  `json:"domain"`
			Path     string  `json:"path"`
			Expires  float64 `json:"expires"`
			HTTPOnly bool    `json:"httpOnly"`
			Secure   bool    `json:"secure"`
		} `json:"cookies"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	out := make([]*http.Cookie, 0, len(r.Cookies))
	for _, c := range r.Cookies {
		//nolint:gosec // a copy of the browser's cookie, attributes as they are
		hc := &http.Cookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path, HttpOnly: c.HTTPOnly, Secure: c.Secure}
		if c.Expires > 0 {
			hc.Expires = time.Unix(int64(c.Expires), 0)
		}
		out = append(out, hc)
	}
	return out, nil
}

// SetCookies gives Chrome cookies set by host (over HTTPS): a domain
// cookie if its Domain is set, else a host-only one.
func (b *Browser) SetCookies(ctx context.Context, host string, cookies []*http.Cookie) error {
	if len(cookies) == 0 {
		return nil
	}
	list := make([]map[string]any, 0, len(cookies))
	for _, c := range cookies {
		ck := map[string]any{"name": c.Name, "value": c.Value, "path": cmp.Or(c.Path, "/"), "secure": c.Secure, "httpOnly": c.HttpOnly}
		if c.Domain != "" {
			ck["domain"] = "." + strings.TrimPrefix(c.Domain, ".")
		} else {
			ck["url"] = "https://" + host + "/"
		}
		if !c.Expires.IsZero() {
			ck["expires"] = c.Expires.Unix()
		}
		list = append(list, ck)
	}
	_, err := b.call(ctx, "", "Storage.setCookies", map[string]any{"cookies": list})
	return err
}

// DeleteCookies removes every cookie whose name is in names and whose host or
// domain matches domainSuffix (both host-only and leading-dot domain cookies).
func (b *Browser) DeleteCookies(ctx context.Context, domainSuffix string, names []string) error {
	return b.DeleteMatchingCookies(ctx, domainSuffix, names, "")
}

// DeleteMatchingCookies expires matching cookies in Chrome. When onlyValue is
// non-empty, only cookies holding that exact value are expired.
func (b *Browser) DeleteMatchingCookies(ctx context.Context, domainSuffix string, names []string, onlyValue string) error {
	suffix := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(domainSuffix), "."))
	if suffix == "" || len(names) == 0 {
		return nil
	}
	cookies, err := b.Cookies(ctx)
	if err != nil {
		return err
	}
	var list []map[string]any
	for _, c := range cookies {
		if !slices.Contains(names, c.Name) {
			continue
		}
		if onlyValue != "" && c.Value != onlyValue {
			continue
		}
		normHost := strings.ToLower(strings.TrimPrefix(c.Domain, "."))
		if normHost != suffix && !strings.HasSuffix(normHost, "."+suffix) {
			continue
		}
		path := cmp.Or(c.Path, "/")
		ck := map[string]any{
			"name":     c.Name,
			"value":    "",
			"path":     path,
			"secure":   c.Secure,
			"httpOnly": c.HttpOnly,
			"expires":  1,
		}
		if strings.HasPrefix(c.Domain, ".") {
			ck["domain"] = c.Domain
		} else {
			scheme := "https"
			if !c.Secure && (strings.EqualFold(normHost, "localhost") || net.ParseIP(normHost) != nil) {
				scheme = "http"
			}
			ck["url"] = scheme + "://" + normHost + path
		}
		list = append(list, ck)
	}
	if len(list) == 0 {
		return nil
	}
	_, err = b.call(ctx, "", "Storage.setCookies", map[string]any{"cookies": list})
	return err
}

// URL returns the page's current address.
func (p *Page) URL(ctx context.Context) string {
	var u string
	_ = p.Eval(ctx, "location.href", &u)
	return strings.TrimSpace(u)
}
