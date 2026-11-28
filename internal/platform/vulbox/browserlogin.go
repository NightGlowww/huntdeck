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
				c.setToken("")
			}
		}()
	}

	err := c.logins.Login(ctx, c.browserLoginFn)
	if err != nil {
		if !errors.Is(err, weblogin.ErrWaiting) {
			c.cred.Refused(err.Error(), weblogin.IsRejection(err))
		}
		c.log.Warn("vulbox: browser sign-in failed", "err", err)
		return err
	}
	tok := c.currentToken()
	if tok == "" {
		return fmt.Errorf("%s: sign-in finished without setting vulbox_token cookie", who)
	}
	succeeded = true
	iat, exp := parseJWTMeta(tok)
	c.cred.SignedIn(parseJWTExpiry(tok))
	c.log.Info("vulbox: browser sign-in succeeded",
		"jwt_len", len(tok),
		"jwt_iat", iat,
		"jwt_exp", exp,
		"has_uuid", c.currentUUID() != "",
	)
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
	startURL := c.web.String() + "/account/login?unique=1&url=" + url.QueryEscape(WebHome)
	userHost := fmt.Sprintf("%q", c.user.Hostname())
	webHost := fmt.Sprintf("%q", c.web.Hostname())
	staleTok := c.currentToken()
	staleLit := fmt.Sprintf("%q", staleTok)
	if c.host != nil && staleTok != "" {
		_ = c.host.ClearCookieValue(ctx, "vulbox.com", []string{tokenCookie}, staleTok)
	}
	var captured []*http.Cookie

	f := weblogin.Flow{
		Who:   who,
		Start: startURL,
		Step: `(() => {
			if (location.protocol === 'chrome-error:') return 'neterror';
			const shown = e => !!e && e.getClientRects().length > 0;
			const vis = s => shown(document.querySelector(s));
			const tokPair = document.cookie.split(';').map(c => c.trim()).find(c => c.startsWith('vulbox_token='));
			const tokVal = tokPair ? tokPair.slice('vulbox_token='.length).trim() : '';
			const hasToken = !!tokVal && tokVal !== 'null' && tokVal !== 'undefined' && tokVal.split('.').length === 3;
			const staleTok = ` + staleLit + `;
			const isStale = !!staleTok && tokVal === staleTok;
			const path = location.pathname.toLowerCase();
			if (location.hostname === ` + userHost + ` && hasToken) {
				return isStale ? 'relogin' : 'in';
			}
			if (location.hostname === ` + webHost + ` && !path.startsWith('/account/login') && !path.startsWith('/account/mfaprotect')) {
				return 'relogin';
			}
			if (path.includes('mfaprotect')) return 'mfa';
			const staleRE = /其他地方被登录|登录失效|请重新登录|会话已过期/i;
			const errEl = [...document.querySelectorAll('.ant-message-error, .el-message--error, .ant-form-explain, .error-msg, .login-error, [role=alert]')]
				.filter(shown).map(e => e.innerText.trim()).filter(t => t && !staleRE.test(t))[0];
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
			if strings.Contains(msg, "其他地方被登录") || strings.Contains(msg, "登录失效") || strings.Contains(msg, "请重新登录") || strings.Contains(msg, "会话已过期") {
				return fmt.Errorf("%s: previous VulBox session was invalidated (%s); retrying cleanly", who, msg)
			}
			if after == "" {
				return fmt.Errorf("%s: the sign-in page shows %q", who, msg)
			}
			return &weblogin.Rejection{Who: who, Msg: msg + " (after " + after + ")"}
		},
		Acts: map[string]weblogin.Act{
			"relogin": {Repeatable: true, Do: func(ctx context.Context, p weblogin.Page) error {
				c.log.Info("vulbox: resetting stale browser session back to login")
				if c.host != nil {
					_ = c.host.ClearCookies(ctx, "vulbox.com", []string{tokenCookie, "authorization"})
				}
				_ = p.Eval(ctx, `(() => {
					for (const name of ['vulbox_token', 'authorization']) {
						document.cookie = name + '=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/; domain=.vulbox.com';
						document.cookie = name + '=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/';
					}
				})()`, nil)
				return p.Navigate(ctx, startURL)
			}},
			"login": {Sends: "the account and password", Do: func(ctx context.Context, p weblogin.Page) error {
				c.log.Info("vulbox: filling login form")
				if err := p.Settle(ctx, 1500*time.Millisecond); err != nil {
					return err
				}
				// Clear any stale/kicked auth cookies across all vulbox.com origins
				// (including host-only cookies on user.vulbox.com / vapi.vulbox.com)
				// before filling credentials so the redirect cannot shadow the fresh cookie.
				if c.host != nil {
					_ = c.host.ClearCookies(ctx, "vulbox.com", []string{tokenCookie, "authorization"})
				}
				_ = p.Eval(ctx, `(() => {
					for (const name of ['vulbox_token', 'authorization']) {
						document.cookie = name + '=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/; domain=.vulbox.com';
						document.cookie = name + '=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/';
					}
				})()`, nil)
				// Tag the visible username/account and password inputs cleanly.
				// VulBox sets readonly="readonly" on #coordinated_username until focused,
				// so do NOT filter out e.readOnly and clear readonly before typing.
				var tagged bool
				err := p.Eval(ctx, `(() => {
					const shown = e => !!e && e.getClientRects().length > 0 && !e.disabled;
					const pw = [
						document.getElementById('coordinated_password'),
						...[...document.querySelectorAll('input[type=password]')],
					].find(shown);
					const user = [
						document.getElementById('coordinated_username'),
						...[...document.querySelectorAll('input[name=username], input[name=account], input[type=text], input[type=tel], input[type=email], input:not([type])')],
					].find(e => shown(e) && e.type !== 'checkbox' && !/captcha|验证码/i.test((e.placeholder || '') + ' ' + (e.name || '') + ' ' + (e.id || '')));
					if (!pw || !user) return false;
					user.removeAttribute('readonly');
					user.readOnly = false;
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

				// Tick VulBox's required user-agreement checkbox so the login button
				// enables cleanly before checking for CAPTCHA / submitting.
				var boxSel string
				_ = p.Eval(ctx, `(() => {
					const shown = e => !!e && e.getClientRects().length > 0;
					const inp = [...document.querySelectorAll('form input[type=checkbox], .ant-checkbox-input')].find(shown);
					if (!inp || inp.checked) return '';
					const box = inp.closest('.ant-checkbox');
					if (shown(box)) {
						box.setAttribute('data-huntdeck-vb-agree', '1');
						return '[data-huntdeck-vb-agree="1"]';
					}
					inp.setAttribute('data-huntdeck-vb-agree', '1');
					return '[data-huntdeck-vb-agree="1"]';
				})()`, &boxSel)
				if boxSel != "" {
					if err := p.Click(ctx, boxSel); err != nil {
						return err
					}
					if err := p.Settle(ctx, 350*time.Millisecond); err != nil {
						return err
					}
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

				// VulBox's "立即登录" button is type="button", not type="submit", so Enter
				// alone does not submit the form; wait briefly for it to enable after
				// ticking the agreement checkbox, then click it.
				var submitSel string
				_, _ = p.WaitFor(ctx, `(() => {
					const shown = e => !!e && e.getClientRects().length > 0 && !e.disabled;
					return [...document.querySelectorAll('button.ant-btn-primary, button[type=submit]')].some(shown) ? 'ready' : '';
				})()`, 2*time.Second)
				_ = p.Eval(ctx, `(() => {
					const shown = e => !!e && e.getClientRects().length > 0 && !e.disabled;
					const btn = [...document.querySelectorAll('button.ant-btn-primary, button[type=submit]')].find(shown);
					if (!btn) return '';
					btn.setAttribute('data-huntdeck-vb-submit', '1');
					return 'button[data-huntdeck-vb-submit="1"]';
				})()`, &submitSel)
				if submitSel == "" {
					return errors.New("VulBox submit button stayed disabled after filling credentials")
				}
				return p.Click(ctx, submitSel)
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
		// page, syncs navigator.userAgent, probes both user.vulbox.com/api and
		// vapi.vulbox.com inside the browser, and seeds c.jar across all VulBox hosts.
		Take: func(ctx context.Context, p weblogin.Page) error {
			_ = p.Settle(ctx, 1500*time.Millisecond)
			type probeRes struct {
				Status int    `json:"status"`
				Code   int    `json:"code"`
				Logged bool   `json:"logged"`
				Msg    string `json:"msg"`
			}
			var snap struct {
				Href      string   `json:"href"`
				Cookie    string   `json:"cookie"`
				UUID      string   `json:"uuid"`
				UA        string   `json:"ua"`
				UserProbe probeRes `json:"userProbe"`
				VapiProbe probeRes `json:"vapiProbe"`
			}
			err := p.Eval(ctx, `(async () => {
				const cookie = document.cookie || '';
				const uuid = (window.localStorage && localStorage.getItem('uuid')) || '';
				let tok = '';
				for (const part of cookie.split(';')) {
					const t = part.trim();
					if (t.startsWith('vulbox_token=')) {
						const v = t.slice('vulbox_token='.length).trim();
						if (v && v !== 'null' && v !== 'undefined' && v.split('.').length === 3) {
							tok = v;
						}
					}
				}
				const probe = async (u, withXRW) => {
					try {
						const headers = { 'Accept': 'application/json, text/plain, */*', 'X-Client-Type': 'web' };
						if (tok) headers['Authorization'] = 'Bearer ' + tok;
						if (uuid) headers['uuid'] = uuid;
						if (withXRW) headers['X-Requested-With'] = 'XMLHttpRequest';
						const r = await fetch(u, { credentials: 'include', headers });
						const j = await r.json().catch(() => ({}));
						return {
							status: r.status || 0,
							code: Number(j.code || 0),
							logged: Boolean(j.data && j.data.logged),
							msg: String(j.msg || '').slice(0, 80),
						};
					} catch (e) {
						return { status: 0, code: 0, logged: false, msg: String(e && e.message ? e.message : e).slice(0, 80) };
					}
				};
				const [userProbe, vapiProbe] = await Promise.all([
					probe('/api/web/account/basic-info', false),
					probe('https://vapi.vulbox.com/web/account/basic-info', true),
				]);
				return {
					href: location.href || '',
					cookie,
					uuid,
					ua: navigator.userAgent || '',
					userProbe,
					vapiProbe,
				};
			})()`, &snap)
			if err != nil {
				return err
			}

			var tok, authVal, cookieUUID string
			if snap.Cookie != "" {
				if parsed, perr := http.ParseCookie(snap.Cookie); perr == nil {
					for _, ck := range parsed {
						switch ck.Name {
						case tokenCookie:
							v := strings.TrimSpace(ck.Value)
							if v != "" && v != "null" && v != "undefined" && strings.Count(v, ".") == 2 {
								tok = v
							}
						case "authorization":
							if v := strings.TrimSpace(ck.Value); v != "" {
								authVal = v
							}
						case uuidCookie:
							if v := strings.TrimSpace(ck.Value); v != "" {
								cookieUUID = v
							}
						}
					}
				}
			}

			var cookies []*http.Cookie
			if tok != "" {
				cookies = append(cookies, &http.Cookie{Name: tokenCookie, Value: tok, Path: "/"}) //nolint:gosec // client-side jar seed
			}
			if authVal != "" {
				cookies = append(cookies, &http.Cookie{Name: "authorization", Value: authVal, Path: "/"}) //nolint:gosec // client-side jar seed
			}
			if finalUUID := firstNonEmpty(snap.UUID, cookieUUID); finalUUID != "" {
				cookies = append(cookies, &http.Cookie{Name: uuidCookie, Value: finalUUID, Path: "/"}) //nolint:gosec // client-side jar seed
			}

			if snap.UA != "" {
				c.setUserAgent(snap.UA)
			}
			iat, exp := parseJWTMeta(tok)
			c.log.Info("vulbox: browser sign-in captured cookies",
				"url", snap.Href,
				"count", len(cookies),
				"jwt_len", len(tok),
				"jwt_iat", iat,
				"jwt_exp", exp,
				"has_uuid", firstNonEmpty(snap.UUID, cookieUUID) != "",
				"ua_synced", snap.UA != "",
				"browser_user_logged", snap.UserProbe.Logged,
				"browser_user_code", snap.UserProbe.Code,
				"browser_user_msg", snap.UserProbe.Msg,
				"browser_vapi_logged", snap.VapiProbe.Logged,
				"browser_vapi_code", snap.VapiProbe.Code,
				"browser_vapi_msg", snap.VapiProbe.Msg,
			)
			if err := validateCapturedSession(tok, staleTok, snap.Href, snap.UserProbe.Logged, snap.VapiProbe.Logged); err != nil {
				return err
			}
			captured = cookies
			c.seedCookies(cookies)
			return nil
		},
	}
	if err := f.Run(ctx, c.host, Name, c.jar); err != nil {
		return err
	}
	// Re-seed fresh cookies captured on user.vulbox.com after weblogin.Flow.keepCookies
	// so host-only stale cookies from Chrome's profile never overwrite them.
	if len(captured) > 0 {
		c.seedCookies(captured)
	}
	return nil
}

func validateCapturedSession(tok, staleTok, href string, userLogged, vapiLogged bool) error {
	if tok == "" {
		return fmt.Errorf("VulBox redirected to %s without setting vulbox_token", href)
	}
	if staleTok != "" && tok == staleTok {
		return errors.New("VulBox still holds the previous rejected vulbox_token")
	}
	if !userLogged && !vapiLogged {
		return fmt.Errorf("VulBox did not accept the browser session at %s (jwt_len=%d, user_logged=%v, vapi_logged=%v)",
			href, len(tok), userLogged, vapiLogged)
	}
	return nil
}
