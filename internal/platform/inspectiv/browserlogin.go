package inspectiv

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/sessionjar"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

const (
	who             = "inspectiv: website login"
	localStorageKey = "inspectiv"
)

// browserSignIn runs the Chrome login through weblogin.History, protecting any
// existing working sessionjar with Checkpoint/Rollback so a failed login
// attempt never wipes a live token.
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
		c.log.Warn("inspectiv: browser sign-in failed", "err", err)
		return err
	}
	tok := c.currentToken()
	if tok == "" {
		return fmt.Errorf("%s: sign-in finished without capturing localStorage[%q]", who, localStorageKey)
	}
	succeeded = true
	c.cred.SignedIn(nil)
	c.log.Info("inspectiv: browser sign-in succeeded", "tok_len", len(tok))
	return nil
}

// browserLogin signs in on https://app.inspectiv.com/#/log-in/ in the shared
// login browser and captures localStorage.getItem("inspectiv").
//
// CAPTCHA safety rule: HuntDeck NEVER attempts to solve, click through, or
// bypass Google reCAPTCHA challenges. If an interactive reCAPTCHA bframe
// iframe becomes visible, browserLogin returns a clear non-sticky error
// directing the user to Settings > Browser (VNC) or INS_TOKEN.
func (c *Client) browserLogin(ctx context.Context) error {
	startURL := strings.TrimSuffix(c.web.String(), "/") + "/#/log-in/"
	var capturedToken string

	f := weblogin.Flow{
		Who:      who,
		Start:    startURL,
		Step:     loginStepJS,
		StepWait: 90 * time.Second,
		Timeout:  4 * time.Minute,
		Sites:    []*url.URL{c.baseOrigin, c.web},
		Refused: func(msg, after string) error {
			low := strings.ToLower(msg)
			if strings.Contains(low, "captcha") || strings.Contains(low, "recaptcha") {
				return fmt.Errorf("%s: %s; open Settings > Browser (VNC) to complete sign-in, or paste INS_TOKEN", who, msg)
			}
			return &weblogin.Rejection{Who: who, Msg: msg + " (after " + cmp.Or(after, "the start") + ")"}
		},
		Acts: map[string]weblogin.Act{
			"login": {
				Sends: "the email and password",
				Do: func(ctx context.Context, p weblogin.Page) error {
					c.log.Info("inspectiv: filling login form")
					if err := p.Settle(ctx, 1200*time.Millisecond); err != nil {
						return err
					}
					if err := checkVisibleRecaptcha(ctx, p); err != nil {
						return err
					}
					if err := p.Fill(ctx, "form.c-signup-form #email", c.account.Email); err != nil {
						return err
					}
					if err := p.Fill(ctx, "form.c-signup-form #password", c.account.Password); err != nil {
						return err
					}
					if err := checkVisibleRecaptcha(ctx, p); err != nil {
						return err
					}
					return p.Click(ctx, "form.c-signup-form button.o-btn--primary")
				},
			},
			"captcha": {
				Repeatable: true,
				Do: func(context.Context, weblogin.Page) error {
					return errors.New("Google reCAPTCHA challenge appeared; open Settings > Browser (VNC) to complete sign-in, or paste INS_TOKEN")
				},
			},
		},
		Take: func(ctx context.Context, p weblogin.Page) error {
			_ = p.Settle(ctx, 1000*time.Millisecond)
			var tok string
			if err := p.Eval(ctx, `(() => {
				const t = (window.localStorage && localStorage.getItem('inspectiv')) || '';
				return t === 'null' || t === 'undefined' ? '' : t.trim();
			})()`, &tok); err != nil {
				return err
			}
			tok = normalizeToken(tok)
			if len(tok) < 20 {
				return fmt.Errorf("localStorage[%q] missing or too short (len=%d)", localStorageKey, len(tok))
			}
			capturedToken = tok
			c.seedToken(tok)
			c.log.Info("inspectiv: captured token from browser", "tok_len", len(tok))
			return nil
		},
	}

	if err := f.Run(ctx, c.host, Name, c.jar); err != nil {
		return err
	}
	if capturedToken != "" {
		c.seedToken(capturedToken)
	}
	return nil
}

const loginStepJS = `(() => {
	if (location.protocol === 'chrome-error:') return 'neterror';
	const shown = e => {
		if (!e || e.getClientRects().length === 0) return false;
		const st = getComputedStyle(e);
		if (st.display === 'none' || st.visibility === 'hidden' || st.opacity === '0') return false;
		const r = e.getBoundingClientRect();
		return r.width > 0 && r.height > 0;
	};
	const vis = s => shown(document.querySelector(s));
	const bframe = [...document.querySelectorAll('iframe[src*="recaptcha/api2/bframe"]')].some(shown);
	if (bframe) return 'captcha';
	const tok = (window.localStorage && localStorage.getItem('inspectiv')) || '';
	const cleanTok = tok === 'null' || tok === 'undefined' ? '' : tok.trim();
	if (cleanTok.length >= 20) return 'in';
	const errEl = [...document.querySelectorAll('form.c-signup-form .error, form.c-signup-form .error-message, form.c-signup-form .o-error, .alert-danger, .toast-error, [role=alert]')]
		.filter(shown)
		.map(e => (e.innerText || '').trim())
		.find(t => t.length > 0);
	if (vis('form.c-signup-form #email') && vis('form.c-signup-form #password')) {
		return errEl ? 'error:' + errEl : 'login';
	}
	if (errEl) return 'error:' + errEl;
	return '';
})()`

func checkVisibleRecaptcha(ctx context.Context, p weblogin.Page) error {
	var challengeVisible bool
	err := p.Eval(ctx, `(() => {
		const shown = e => {
			if (!e || e.getClientRects().length === 0) return false;
			const st = getComputedStyle(e);
			if (st.display === 'none' || st.visibility === 'hidden' || st.opacity === '0') return false;
			const r = e.getBoundingClientRect();
			return r.width > 0 && r.height > 0;
		};
		return [...document.querySelectorAll('iframe[src*="recaptcha/api2/bframe"]')].some(shown);
	})()`, &challengeVisible)
	if err != nil {
		return err
	}
	if challengeVisible {
		return errors.New("Google reCAPTCHA challenge appeared; open Settings > Browser (VNC) to complete sign-in, or paste INS_TOKEN")
	}
	return nil
}
