package vulbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/sessionjar"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// who prefixes all VulBox website-login errors.
const who = "vulbox: website login"

// browserSignIn runs the Chrome login through weblogin.History, protecting any
// existing working sessionjar with Checkpoint/Rollback so a failed login
// attempt never wipes a live session.
func (c *Client) browserSignIn(ctx context.Context) error {
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

	succeeded := false
	if cp != nil && len(checkpoint) > 0 {
		defer func() {
			if !succeeded || c.currentToken() == "" {
				_ = cp.Rollback(checkpoint)
			}
		}()
	}

	err := c.logins.Login(ctx, c.browserLoginFn)
	if err != nil {
		if !errors.Is(err, weblogin.ErrWaiting) {
			c.cred.Refused(err.Error(), weblogin.IsRejection(err))
		}
		return err
	}
	if c.currentToken() == "" {
		return fmt.Errorf("%s: sign-in finished without setting vulbox_token cookie", who)
	}
	succeeded = true
	c.cred.SignedIn(parseJWTExpiry(c.currentToken()))
	return nil
}

// browserLogin signs in on https://www.vulbox.com/account/login in the shared
// login browser.
//
// CAPTCHA safety rule: HuntDeck NEVER attempts to solve, click through, or
// bypass VulBox's image CAPTCHA. If a CAPTCHA is visible after filling the
// account and password fields, browserLogin returns a clear non-sticky error
// telling the user to complete CAPTCHA in Settings > Browser (VNC) or paste
// VB_SESSION.
func (c *Client) browserLogin(ctx context.Context) error {
	startURL := c.web.String() + "/account/login?url=" + url.QueryEscape(WebHome)
	webHost := fmt.Sprintf("%q", c.web.Hostname())
	userHost := fmt.Sprintf("%q", c.user.Hostname())

	f := weblogin.Flow{
		Who:   who,
		Start: startURL,
		Step: `(() => {
			if (location.protocol === 'chrome-error:') return 'neterror';
			const shown = e => !!e && e.getClientRects().length > 0;
			const vis = s => shown(document.querySelector(s));
			const hasToken = document.cookie.split(';').some(c => c.trim().startsWith('vulbox_token=') && c.trim().length > 'vulbox_token='.length);
			const path = location.pathname.toLowerCase();
			if ((location.hostname === ` + userHost + ` || (location.hostname === ` + webHost + ` && !path.startsWith('/account/login') && !path.startsWith('/account/mfaprotect'))) && hasToken) {
				return 'in';
			}
			if (path.includes('mfaprotect')) return 'mfa';
			const errEl = [...document.querySelectorAll('.ant-message-error, .el-message--error, .error-msg, .login-error, [role=alert]')]
				.filter(shown).map(e => e.innerText.trim()).filter(Boolean)[0];
			if (vis('input[type=password]')) {
				return errEl ? 'error:' + errEl : 'login';
			}
			if (errEl) return 'error:' + errEl;
			return '';
		})()`,
		StepWait: 90 * time.Second,
		Timeout:  4 * time.Minute,
		Sites:    []*url.URL{c.base, c.web, c.user, c.root},
		Refused: func(msg, after string) error {
			low := strings.ToLower(msg)
			if strings.Contains(low, "captcha") || strings.Contains(msg, "验证码") {
				return fmt.Errorf("%s: VulBox requires an image CAPTCHA (%s); open Settings > Browser (VNC) to finish signing in, or paste VB_SESSION", who, msg)
			}
			if after == "" {
				return fmt.Errorf("%s: the sign-in page shows %q", who, msg)
			}
			return &weblogin.Rejection{Who: who, Msg: msg + " (after " + after + ")"}
		},
		Acts: map[string]weblogin.Act{
			"login": {Sends: "the account and password", Do: func(ctx context.Context, p weblogin.Page) error {
				if err := p.Settle(ctx, 1500*time.Millisecond); err != nil {
					return err
				}
				// Tag the visible username/account and password inputs cleanly.
				var tagged bool
				err := p.Eval(ctx, `(() => {
					const shown = e => !!e && e.getClientRects().length > 0 && !e.disabled && !e.readOnly;
					const pw = [...document.querySelectorAll('input[type=password]')].find(shown);
					const user = [...document.querySelectorAll('input[name=username], input[name=account], input[type=text], input[type=tel], input[type=email]')].find(e => shown(e) && !/captcha|验证码/i.test((e.placeholder || '') + ' ' + (e.name || '')));
					if (!pw || !user) return false;
					user.setAttribute('data-huntdeck-vb', 'user');
					pw.setAttribute('data-huntdeck-vb', 'pw');
					return true;
				})()`, &tagged)
				if err != nil {
					return err
				}
				if !tagged {
					return errors.New("could not locate VulBox account and password inputs")
				}
				if err := p.Fill(ctx, `input[data-huntdeck-vb=user]`, c.account.Email); err != nil {
					return err
				}
				if err := p.Fill(ctx, `input[data-huntdeck-vb=pw]`, c.account.Password); err != nil {
					return err
				}

				// Check if an image CAPTCHA is visible; NEVER attempt to solve or bypass it.
				var hasCaptcha bool
				_ = p.Eval(ctx, `(() => {
					const shown = e => !!e && e.getClientRects().length > 0;
					const img = [...document.querySelectorAll('img[src^="data:image"], img[alt*="验证码"], .captcha img')].some(shown);
					const inp = [...document.querySelectorAll('input')].some(e => shown(e) && /captcha|验证码/i.test((e.placeholder || '') + ' ' + (e.name || '') + ' ' + (e.id || '')));
					return img || inp;
				})()`, &hasCaptcha)
				if hasCaptcha {
					return errors.New("VulBox requires an image CAPTCHA; open Settings > Browser (VNC) to complete sign-in (account and password are already filled), or paste VB_SESSION")
				}
				return p.Press(ctx)
			}},
			"mfa": {Sends: "the 2FA code", Do: func(ctx context.Context, p weblogin.Page) error {
				if c.account.TOTPSecret == "" {
					return &weblogin.Rejection{Who: who, Msg: "VulBox asked for a 2FA code (/account/Mfaprotect); set VB_TOTP_SECRET"}
				}
				code, err := c.account.Code(ctx)
				if err != nil {
					return err
				}
				var found bool
				err = p.Eval(ctx, `(() => {
					const shown = e => !!e && e.getClientRects().length > 0 && !e.disabled && !e.readOnly;
					const inp = [...document.querySelectorAll('input[type=text], input[type=tel], input[type=number], input:not([type])')].find(shown);
					if (!inp) return false;
					inp.setAttribute('data-huntdeck-vb', 'mfa');
					return true;
				})()`, &found)
				if err != nil {
					return err
				}
				if !found {
					return errors.New("could not locate VulBox 2FA input on /account/Mfaprotect")
				}
				if err := p.Fill(ctx, `input[data-huntdeck-vb=mfa]`, code); err != nil {
					return err
				}
				return p.Press(ctx)
			}},
		},
		// Take extracts vulbox_token, authorization, and uuid from the signed-in
		// page and seeds c.jar across all VulBox hosts (including vapi.vulbox.com)
		// because .vulbox.com cookies may otherwise only match root in keepCookies.
		Take: func(ctx context.Context, p weblogin.Page) error {
			var snap struct {
				Cookie string `json:"cookie"`
				UUID   string `json:"uuid"`
			}
			if err := p.Eval(ctx, `({cookie: document.cookie || '', uuid: (window.localStorage && localStorage.getItem('uuid')) || ''})`, &snap); err != nil {
				return err
			}
			var cookies []*http.Cookie
			if snap.Cookie != "" {
				if parsed, err := http.ParseCookie(snap.Cookie); err == nil {
					for _, ck := range parsed {
						if ck.Name == tokenCookie || ck.Name == uuidCookie || ck.Name == "authorization" {
							ck.Path = "/"
							cookies = append(cookies, ck)
						}
					}
				}
			}
			if snap.UUID != "" {
				cookies = append(cookies, &http.Cookie{Name: uuidCookie, Value: snap.UUID, Path: "/"}) //nolint:gosec // client-side jar seed
			}
			if len(cookies) > 0 {
				c.seedCookies(cookies)
			}
			return nil
		},
	}
	return f.Run(ctx, c.host, Name, c.jar)
}
