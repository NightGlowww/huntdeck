package gobugfree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/sessionjar"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// who prefixes all GObugfree website-login errors.
const who = "gobugfree: website login"

// browserSignInLocked runs the Chrome login through weblogin.History, protecting
// any existing working sessionjar and in-memory tokens with Checkpoint/Rollback
// so a failed login attempt never wipes a live session. Caller must hold c.authMu.
func (c *Client) browserSignInLocked(ctx context.Context) error {
	if c.logins == nil {
		return fmt.Errorf("%s: login browser is not configured", who)
	}
	if err := c.logins.Stuck(); err != nil {
		return err
	}
	if c.logins.Waiting() {
		return fmt.Errorf("%s: a recent login failed; keeping current session until retry window (%w)", who, weblogin.ErrWaiting)
	}

	var cp *sessionjar.Jar
	var checkpoint []byte
	if jar, ok := c.jar.(*sessionjar.Jar); ok {
		cp = jar
		checkpoint = cp.Checkpoint()
	}
	oldAccess, oldRefresh := c.tokens()

	succeeded := false
	defer func() {
		if succeeded {
			return
		}
		if cp != nil && len(checkpoint) > 0 {
			_ = cp.Rollback(checkpoint)
		}
		c.setTokens(oldAccess, oldRefresh)
	}()

	err := c.logins.Login(ctx, c.browserLoginFn)
	if err != nil {
		if !errors.Is(err, weblogin.ErrWaiting) {
			c.cred.Refused(err.Error(), weblogin.IsRejection(err))
		}
		c.log.Warn("gobugfree: browser sign-in failed", "err", err)
		return err
	}

	ok, reason, err := c.sessionCheck(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s: sign-in finished but GObugfree did not accept the session (%s)", who, reason)
	}

	succeeded = true
	access, refresh := c.tokens()
	var exp *time.Time
	if refresh != "" {
		exp = parseJWTExpiry(refresh)
	}
	if exp == nil && access != "" {
		exp = parseJWTExpiry(access)
	}
	c.cred.SignedIn(exp)
	c.log.Info("gobugfree: browser sign-in succeeded",
		"has_bearer", access != "",
		"has_refresh", refresh != "",
		"has_cookie", c.hasSessionCookie(),
	)
	return nil
}

// browserLogin signs in via {BaseURL}/auth/login -> Keycloak (id.gobugfree.com),
// captures the session cookie from app.gobugfree.com, and falls back to the
// RFC 8628 device authorization flow if the session cookie alone returns HTML
// instead of JSON on GET /settings.
//
// CAPTCHA safety rule: HuntDeck NEVER solves, clicks through, or bypasses a
// CAPTCHA. If a CAPTCHA is present on the login form, it returns a clear error
// so the user can sign in via Settings > Browser (VNC) or set GBF_TOKEN.
func (c *Client) browserLogin(ctx context.Context) error {
	startURL := c.base.String() + "/auth/login"
	baseHost := fmt.Sprintf("%q", c.base.Hostname())
	idHost := fmt.Sprintf("%q", c.id.Hostname())

	f := weblogin.Flow{
		Who:   who,
		Start: startURL,
		Step: `(() => {
			if (location.protocol === 'chrome-error:') return 'neterror';
			const shown = e => !!e && e.getClientRects().length > 0;
			const vis = s => shown(document.querySelector(s));
			const host = location.hostname;
			const path = location.pathname.toLowerCase();

			if (host === ` + baseHost + ` && !path.startsWith('/auth')) {
				return 'in';
			}

			const hasCaptcha = vis('.g-recaptcha, iframe[src*="recaptcha"], iframe[src*="turnstile"], iframe[src*="hcaptcha"], .cf-turnstile, #kc-captcha');
			if (hasCaptcha) return 'error:CAPTCHA required on login page';

			const errText = [...document.querySelectorAll('.alert-error, .kc-feedback-text, #input-error, #input-error-username, #input-error-password, #input-error-otp-code, .pf-c-alert.pf-m-danger, [role=alert]')]
				.filter(shown).map(e => e.innerText.trim()).filter(Boolean)[0];

			if (vis('#otp, input[name=otp], input[name=totp]')) {
				return errText ? 'error:' + errText : 'totp';
			}
			if (vis('#password, input[name=password], input[type=password]')) {
				return errText ? 'error:' + errText : 'login';
			}
			if (vis('#username, input[name=username]')) {
				return errText ? 'error:' + errText : 'username';
			}
			if (host === ` + idHost + ` && errText) {
				return 'error:' + errText;
			}
			return '';
		})()`,
		StepWait: 90 * time.Second,
		Timeout:  4 * time.Minute,
		Sites:    []*url.URL{c.base, c.id},
		Refused: func(msg, after string) error {
			low := strings.ToLower(msg)
			if strings.Contains(low, "captcha") {
				return fmt.Errorf("%s: GObugfree requires a CAPTCHA (%s); complete sign-in in Settings > Browser (VNC) or set GBF_TOKEN", who, msg)
			}
			if after == "" {
				return fmt.Errorf("%s: the sign-in page shows %q", who, msg)
			}
			return &weblogin.Rejection{Who: who, Msg: msg + " (after " + after + ")"}
		},
		Acts: map[string]weblogin.Act{
			"username": {Sends: "the email", Repeatable: true, Do: func(ctx context.Context, p weblogin.Page) error {
				if err := p.Settle(ctx, 500*time.Millisecond); err != nil {
					return err
				}
				if err := p.Fill(ctx, "#username, input[name=username]", c.account.Email); err != nil {
					return err
				}
				return clickKeycloakSubmit(ctx, p)
			}},
			"login": {Sends: "the email and password", Do: func(ctx context.Context, p weblogin.Page) error {
				if err := p.Settle(ctx, 500*time.Millisecond); err != nil {
					return err
				}
				var hasUser bool
				_ = p.Eval(ctx, `(() => {
					const e = document.querySelector('#username, input[name=username]');
					return !!e && e.getClientRects().length > 0;
				})()`, &hasUser)
				if hasUser {
					if err := p.Fill(ctx, "#username, input[name=username]", c.account.Email); err != nil {
						return err
					}
				}
				if err := p.Fill(ctx, "#password, input[name=password], input[type=password]", c.account.Password); err != nil {
					return err
				}
				p.Remember(ctx)
				return clickKeycloakSubmit(ctx, p)
			}},
			"totp": {Sends: "the 2FA code", Do: func(ctx context.Context, p weblogin.Page) error {
				code, err := c.account.Code(ctx)
				if err != nil {
					return err
				}
				if err := p.Fill(ctx, "#otp, input[name=otp], input[name=totp]", code); err != nil {
					return err
				}
				return clickKeycloakSubmit(ctx, p)
			}},
		},
	}
	if err := f.Run(ctx, c.host, Name, c.jar); err != nil {
		return err
	}

	// Verify whether the captured session cookie works directly for JSON API calls.
	// If GET /settings returns HTML (Keycloak proxy behavior), fall back to
	// RFC 8628 device authorization inside the signed-in browser session.
	ok, _, err := c.sessionCheck(ctx)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	return c.deviceFlowLogin(ctx)
}

func clickKeycloakSubmit(ctx context.Context, p weblogin.Page) error {
	var sel string
	_ = p.Eval(ctx, `(() => {
		const shown = e => !!e && e.getClientRects().length > 0 && !e.disabled;
		const btn = ['#kc-login', 'button[type=submit]', 'input[type=submit]'].map(s => document.querySelector(s)).find(shown);
		if (!btn) return '';
		btn.setAttribute('data-huntdeck-gbf-submit', '1');
		return '[data-huntdeck-gbf-submit="1"]';
	})()`, &sel)
	if sel != "" {
		return p.Click(ctx, sel)
	}
	return p.Press(ctx)
}

type deviceAuthResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// deviceFlowLogin runs Keycloak's RFC 8628 device flow using the already-signed-in
// browser tab to approve the agent client and obtains OAuth access + refresh tokens.
func (c *Client) deviceFlowLogin(ctx context.Context) error {
	form := url.Values{
		"client_id": {defaultClientID},
		"scope":     {"openid profile"},
	}
	authURL := c.id.String() + "/protocol/openid-connect/auth/device"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	res, err := c.action.Do(ctx, req)
	if err != nil {
		return fmt.Errorf("%s: device authorization: %w", who, err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("%s: device authorization HTTP %d", who, res.StatusCode)
	}
	var da deviceAuthResponse
	if err := json.Unmarshal(res.Body, &da); err != nil {
		return fmt.Errorf("%s: decode device authorization: %w", who, err)
	}
	if da.DeviceCode == "" || firstNonEmpty(da.VerificationURIComplete, da.VerificationURI) == "" {
		return fmt.Errorf("%s: device authorization missing device_code or verification_uri", who)
	}

	verifyURL := firstNonEmpty(da.VerificationURIComplete, da.VerificationURI)
	if err := c.deviceLoginFn(ctx, verifyURL); err != nil {
		return err
	}

	interval := time.Duration(da.Interval) * time.Second
	if interval <= 0 {
		interval = 2 * time.Second
	}
	expires := time.Duration(da.ExpiresIn) * time.Second
	if expires <= 0 {
		expires = 2 * time.Minute
	}
	deadline := c.now().Add(expires)

	for c.now().Before(deadline) {
		if err := c.sleep(ctx, interval); err != nil {
			return err
		}
		tokForm := url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"client_id":   {defaultClientID},
			"device_code": {da.DeviceCode},
		}
		tr, status, err := c.postTokenForm(ctx, tokForm)
		if err == nil {
			c.setTokens(tr.AccessToken, tr.RefreshToken)
			return nil
		}
		switch tr.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 2 * time.Second
			continue
		default:
			if status == http.StatusBadRequest && tr.Error != "" {
				return fmt.Errorf("%s: device token exchange failed (%s): %w", who, tr.Error, err)
			}
			return err
		}
	}
	return fmt.Errorf("%s: device authorization timed out waiting for token", who)
}

func (c *Client) runDeviceFlowBrowser(ctx context.Context, verifyURL string) error {
	f := weblogin.Flow{
		Who:   who + " (device grant)",
		Start: verifyURL,
		Step: `(() => {
			if (location.protocol === 'chrome-error:') return 'neterror';
			const shown = e => !!e && e.getClientRects().length > 0;
			const vis = s => shown(document.querySelector(s));
			const text = (document.body && document.body.innerText || '').toLowerCase();
			if (/device login successful|you may close|authorized|approved|completed/i.test(text)) {
				return 'in';
			}
			if (vis('#kc-login, input[name=accept], button[name=accept], button[type=submit], input[type=submit]')) {
				return 'grant';
			}
			return '';
		})()`,
		StepWait: 30 * time.Second,
		Timeout:  90 * time.Second,
		Sites:    []*url.URL{c.base, c.id},
		Acts: map[string]weblogin.Act{
			"grant": {Repeatable: true, Do: func(ctx context.Context, p weblogin.Page) error {
				var sel string
				_ = p.Eval(ctx, `(() => {
					const shown = e => !!e && e.getClientRects().length > 0 && !e.disabled;
					const btn = ['#kc-login', 'input[name=accept]', 'button[name=accept]', 'button[type=submit]', 'input[type=submit]']
						.map(s => document.querySelector(s)).find(shown);
					if (!btn) return '';
					btn.setAttribute('data-huntdeck-gbf-grant', '1');
					return '[data-huntdeck-gbf-grant="1"]';
				})()`, &sel)
				if sel != "" {
					return p.Click(ctx, sel)
				}
				return p.Press(ctx)
			}},
		},
	}
	return f.Run(ctx, c.host, Name, c.jar)
}
