package intigriti

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/sessionjar"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// The researcher web app (app.intigriti.com) shows more than the API: stats
// for every program you can open, response times and a metrics summary of
// all programs. It runs on a cookie session from Intigriti's OpenID Connect
// login site (verified live 2026-10-28):
//
//   - login.intigriti.com/Account/Login takes the email, then the password
//     with a reCAPTCHA v3 token (a plain HTTP client is answered "Bot-like
//     behavior detected"), then a TOTP code at /account/loginwith2fa. So the
//     first login runs in headless Chrome (package browser), which passes.
//   - That leaves a sign-on cookie (__Host-Intigriti.SSO) on the login site.
//     With it, app.intigriti.com/auth/researcher signs in without a password:
//     the login site answers with an auto-posting form (OIDC form_post) to
//     /signin-oidc, then again to /signin-oidc-researcher, after which the
//     app sets __Host-Intigriti.Web.Researcher (the session) and
//     __Host-Intigriti.CsrfToken.Researcher (echoed as X-Csrf-Token on
//     POSTs). HuntDeck does that over plain HTTP whenever the app session
//     lapses, and only opens Chrome again once the sign-on cookie has too.

const (
	loginHost        = "login.intigriti.com"
	appHost          = "app.intigriti.com"
	appSessionCookie = "__Host-Intigriti.Web.Researcher"
	csrfCookie       = "__Host-Intigriti.CsrfToken.Researcher"
	webAPI           = "/api/core/researcher"

	// who starts the login's errors.
	who = "intigriti: login"
)

// webSession holds what the logged-in calls need. Its mutex serialises
// logins.
type webSession struct {
	mu      sync.Mutex
	account *weblogin.Account
	host    *weblogin.Host // the shared login browser
	history *weblogin.History
	jar     http.CookieJar
	app     *url.URL // the web app's base URL
	login   func(ctx context.Context) error
}

// rejected is Intigriti refusing the login; it sticks until a restart.
func rejected(msg string) error { return &weblogin.Rejection{Who: who, Msg: msg} }

// ErrNoSession means logged-in data was asked for without IT_EMAIL and
// IT_PASSWORD.
var ErrNoSession = fmt.Errorf("intigriti: %w (IT_EMAIL, IT_PASSWORD)", platform.ErrNoWebLogin)

func (c *Client) loggedIn() bool { return c.web != nil }

// appCookie returns one of the web app's cookies, or "".
func (s *webSession) appCookie(name string) string {
	for _, ck := range s.jar.Cookies(s.app) {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

// ensure makes sure there is an app session: the one in the jar, else one
// signed in silently with the sign-on cookie, else a browser login. force
// skips the first, after the app refused the session.
func (c *Client) ensureSession(ctx context.Context, force bool) error {
	s := c.web
	if s == nil {
		return ErrNoSession
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.history.Stuck(); err != nil {
		return err
	}
	if !force && s.appCookie(appSessionCookie) != "" {
		return nil
	}
	// A login must not be able to damage a session that already works: the
	// platform answers a refused attempt by clearing the session cookie, force
	// clears it here first, and a login held in a real browser copies its own
	// cookies into this jar after the flow ends. So the jar is checkpointed and
	// put back when the attempt leaves no session - memory and file, which is the
	// only "as it was" a late write cannot race.
	if cp, isJar := s.jar.(*sessionjar.Jar); isJar {
		checkpoint := cp.Checkpoint()
		if len(checkpoint) > 0 {
			defer func() {
				if !sessionProduced(s.jar, s.app) {
					_ = cp.Rollback(checkpoint)
				}
			}()
		}
	}
	if force {
		// Dropping the session is only worth doing when a login will actually run.
		// With the last one failed under RetryAfter ago, the attempt below returns at
		// once - and a live session would have been thrown away for nothing. That is
		// how a working session vanished at 09:19 after a refused login at 09:05.
		if s.history.Waiting() {
			return fmt.Errorf("%s: the session was refused, but a login failed recently: keeping the session%s", who, waitingHint(s.history))
		}
		// The app refused it: drop it, so the app starts a new one.
		s.jar.SetCookies(s.app, []*http.Cookie{{Name: appSessionCookie, Path: "/", MaxAge: -1}}) //nolint:gosec // deletes it from the jar
	}
	ok, err := c.signIn(ctx)
	if err != nil {
		return err
	}
	if ok {
		s.history.Renewed()
		return nil
	}
	if err := s.history.Login(ctx, s.login); err != nil {
		return err
	}
	if s.appCookie(appSessionCookie) == "" {
		// The browser stopped short of the app; finish over HTTP.
		if ok, err := c.signIn(ctx); err != nil || !ok {
			return errors.Join(errors.New("intigriti: logged in, but the app didn't start a session"), err)
		}
	}
	return nil
}

var (
	formRE  = regexp.MustCompile(`(?is)<form\b[^>]*\baction=['"]([^'"]+)['"][^>]*>(.*?)</form>`)
	inputRE = regexp.MustCompile(`(?is)<input\b[^>]*>`)
	attrRE  = regexp.MustCompile(`(?is)\b(name|value)=['"]([^'"]*)['"]`)
)

// signIn starts an app session with the sign-on cookie, posting the login
// site's form_post answers back to the app. ok is false when the login site
// asks for a password instead: the sign-on cookie is missing or expired.
func (c *Client) signIn(ctx context.Context) (ok bool, err error) {
	s := c.web
	u := s.app.String() + "/auth/researcher"
	method, body := http.MethodGet, url.Values(nil)
	for range 6 {
		res, err := c.webDo(ctx, method, u, body, "")
		if err != nil {
			return false, fmt.Errorf("intigriti: sign in: %w", err)
		}
		page := string(res.Body)
		if res.StatusCode != http.StatusOK {
			return false, fmt.Errorf("intigriti: sign in: HTTP %d", res.StatusCode)
		}
		if strings.Contains(page, "Input.Email") || strings.Contains(page, "Input.Password") {
			return false, nil
		}
		m := formRE.FindStringSubmatch(page)
		if m == nil {
			return s.appCookie(appSessionCookie) != "", nil
		}
		action, err := url.Parse(html.UnescapeString(m[1]))
		if err != nil || action.Host != s.app.Host || !strings.HasPrefix(action.Path, "/signin-oidc") {
			return false, fmt.Errorf("intigriti: sign in: unexpected form to %s", action.Redacted())
		}
		body = url.Values{}
		for _, in := range inputRE.FindAllString(m[2], -1) {
			var name, value string
			for _, a := range attrRE.FindAllStringSubmatch(in, -1) {
				if strings.EqualFold(a[1], "name") {
					name = html.UnescapeString(a[2])
				} else {
					value = html.UnescapeString(a[2])
				}
			}
			if name != "" {
				body.Set(name, value)
			}
		}
		method, u = http.MethodPost, action.String()
	}
	return false, errors.New("intigriti: sign in: too many steps")
}

// webDo sends one request to the web app with the session's cookies.
func (c *Client) webDo(ctx context.Context, method, u string, form url.Values, jsonBody string) (*httpResponse, error) {
	var rd *strings.Reader
	switch {
	case form != nil:
		rd = strings.NewReader(form.Encode())
	case jsonBody != "":
		rd = strings.NewReader(jsonBody)
	}
	var req *http.Request
	var err error
	if rd != nil {
		req, err = http.NewRequestWithContext(ctx, method, u, rd)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, u, nil)
	}
	if err != nil {
		return nil, err
	}
	switch {
	case form != nil:
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "https://"+loginHost)
	case jsonBody != "":
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		if v := c.web.appCookie(csrfCookie); v != "" {
			if dv, err := url.QueryUnescape(v); err == nil {
				v = dv
			}
			req.Header.Set("X-Csrf-Token", v)
		}
	default:
		req.Header.Set("Accept", "application/json, text/html;q=0.9")
	}
	res, err := c.http.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	return &httpResponse{StatusCode: res.StatusCode, Header: res.Header, Body: res.Body}, nil
}

type httpResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// webJSON calls a researcher web API path (under /api/core/researcher) and
// decodes its JSON. A refused session is renewed once. 403 (a program you
// can't open) wraps platform.ErrScopeUnavailable.
func (c *Client) webJSON(ctx context.Context, method, path string, body, into any) error {
	if err := c.ensureSession(ctx, false); err != nil {
		return err
	}
	var payload string
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = string(b)
	}
	u := c.web.app.String() + webAPI + path
	for attempt := 0; ; attempt++ {
		res, err := c.webDo(ctx, method, u, nil, payload)
		if err != nil {
			return err
		}
		switch {
		case res.StatusCode == http.StatusUnauthorized && attempt == 0:
			if err := c.ensureSession(ctx, true); err != nil {
				return err
			}
			continue
		case res.StatusCode == http.StatusForbidden:
			return fmt.Errorf("HTTP 403: %w", platform.ErrScopeUnavailable)
		case res.StatusCode != http.StatusOK:
			return fmt.Errorf("HTTP %d", res.StatusCode)
		case !strings.Contains(res.Header.Get("Content-Type"), "json"):
			return errors.New("not JSON")
		}
		return json.NewDecoder(bytes.NewReader(res.Body)).Decode(into)
	}
}

// browserLogin signs in with Chrome, windowed, as a person would: the
// email, then the password (sent with a reCAPTCHA token) with "Remember
// me" ticked, then a TOTP code. It keeps the login site's sign-on cookie
// and the app's session.
func (c *Client) browserLogin(ctx context.Context) error {
	s := c.web
	f := weblogin.Flow{
		Who:   who,
		Start: s.app.String() + "/auth/researcher",
		Step: `(() => {
			if (location.protocol === 'chrome-error:') return 'neterror';
			const vis = s => { const e = document.querySelector(s); return e && e.offsetParent !== null && !e.readOnly };
			const err = [...document.querySelectorAll('.text-danger, .validation-summary-errors')].map(e => e.innerText.trim()).filter(Boolean)[0];
			if (location.host === '` + appHost + `' && !location.pathname.startsWith('/auth')) return 'in';
			if (err) return 'error:' + err;
			if (vis("input[name='Input.TwoFactorAuthentication.VerificationCode']")) return '2fa';
			if (vis("input[name='Input.Password']")) return 'password';
			if (vis("input[name='Input.Email']")) return 'email';
			return '';
		})()`,
		Sites: []*url.URL{{Scheme: "https", Host: appHost, Path: "/"}, {Scheme: "https", Host: loginHost, Path: "/"}},
		Refused: func(msg, after string) error {
			if strings.Contains(strings.ToLower(msg), "bot") {
				// The page scores the visitor, not the password: say what changes
				// the score, or the next reader tries the same login again.
				return fmt.Errorf("%s: the login page took Chrome for a bot (%s): set HUNTDECK_PROXY to a "+
					"residential proxy and HUNTDECK_CHROME_TIMEZONE to its country, and HUNTDECK_VNC=true to "+
					"watch the browser", who, msg)
			}
			if after == "" {
				after = "the start"
			}
			return rejected(msg + " (after " + after + ")")
		},
		Acts: map[string]weblogin.Act{
			"email": {Sends: "the email", Repeatable: true, Do: func(ctx context.Context, p weblogin.Page) error {
				if err := p.Settle(ctx, 2*time.Second); err != nil {
					return err
				}
				if err := p.Fill(ctx, "input[name='Input.Email']", s.account.Email); err != nil {
					return err
				}
				p.Remember(ctx)

				return p.Press(ctx)
			}},
			"password": {Sends: "the password", Do: func(ctx context.Context, p weblogin.Page) error {
				// The password goes with a reCAPTCHA token: give its script
				// time to load and watch the page, as a person would.
				_, _ = p.WaitFor(ctx, "window.grecaptcha && typeof grecaptcha.execute === 'function' && 'ready'", 20*time.Second)
				if err := p.Settle(ctx, 2500*time.Millisecond); err != nil {
					return err
				}
				p.Tick(ctx, "input[name='Input.RememberLogin']") // so the sign-on cookie lasts
				if err := p.Fill(ctx, "input[name='Input.Password']", s.account.Password); err != nil {
					return err
				}
				p.Remember(ctx)

				return p.Press(ctx)
			}},
			"2fa": {Sends: "the 2FA code", Do: func(ctx context.Context, p weblogin.Page) error {
				code, err := s.account.Code(ctx)
				if err != nil {
					return err
				}
				p.Tick(ctx, "input[name='Input.RememberMachine']")
				if err := p.Fill(ctx, "input[name='Input.TwoFactorAuthentication.VerificationCode']", code); err != nil {
					return err
				}
				p.Remember(ctx)

				return p.Press(ctx)
			}},
		},
	}
	return f.Run(ctx, s.host, Name, s.jar)
}

// restore puts the cookies back exactly as they were.
//
// Setting the saved ones is not enough: a refused login leaves its own cookies
// behind too, and the first version of this function kept them - the test below
// caught it. Anything the attempt added has to go, or "as it was" means a jar
// holding a live session plus a stranger's crumbs.

// sessionProduced reports whether the jar holds a session worth keeping. A login
// that ends without one is a login to undo, whatever error it returned.
func sessionProduced(jar http.CookieJar, app *url.URL) bool {
	for _, c := range jar.Cookies(app) {
		if c.Name == appSessionCookie && c.Value != "" {
			return true
		}
	}
	return false
}

// waitingHint names when the next login may run, without repeating the error the
// caller already carries.
func waitingHint(h *weblogin.History) string {
	if err := h.Stuck(); err != nil {
		return " (a rejection holds until a restart)"
	}
	return ""
}
