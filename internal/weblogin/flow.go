package weblogin

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/browser"
)

// Done is the step of a signed-in page.
const Done = "in"

// Flow is a sign-in walked page by page, as a person would: Chrome opens
// Start, Step names the step each page is at, and Acts answers it, until
// a page is signed in.
type Flow struct {
	Who   string // starts errors: "hackerone: website login"
	Start string // the URL it starts at
	// Step is a JavaScript expression naming the step the page is at: a
	// key of Acts; Done; "error:" and the page's message; "neterror" for
	// Chrome's own error page (as when a network change cuts a request
	// off); or "" while it can't tell yet.
	Step string
	Acts map[string]Act
	// Pass are steps that move on by themselves, waited out: a bot check.
	Pass     []string
	StepWait time.Duration // how long a page may sit on one step; default 60s
	Timeout  time.Duration // the whole login; default 3m
	// Refused is the error for a page's error message, after what was
	// sent last ("" at the start). By default, a Rejection.
	Refused func(msg, after string) error
	// Sites are where the cookies the jar gets once signed in are from.
	Sites []*url.URL
	// Take, if set, runs on the signed-in page before its cookies are
	// kept: for a session a web app holds outside cookies (a token in its
	// localStorage).
	Take func(ctx context.Context, p Page) error
	// Pause sleeps between polls of the page; tests shorten it.
	Pause func(context.Context, time.Duration) error
}

// Act answers one step.
type Act struct {
	Sends string // what it sends, for errors: "the password"
	// Repeatable means repeating it can't count as another attempt (an
	// email, not a password or code): after a dropped connection the
	// login starts over rather than failing.
	Repeatable bool
	Do         func(ctx context.Context, p Page) error
}

// Run signs in in the shared browser's tab of that name, and copies the
// Sites' cookies into jar. The browser keeps its profile, and with it a
// remembered login, so the tab may find itself signed in already. One
// login runs at a time, with its tab at the front.
func (f Flow) Run(ctx context.Context, host *Host, tab string, jar http.CookieJar) error {
	timeout := f.Timeout
	if timeout == 0 {
		timeout = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	host.login.Lock()
	defer host.login.Unlock()
	bp, _, err := host.Tab(ctx, tab)
	if err != nil {
		return fmt.Errorf("%s: %w", f.Who, err)
	}
	_ = bp.Activate(ctx)
	p := Page{bp}
	if err := p.Navigate(ctx, f.Start); err != nil {
		return fmt.Errorf("%s: %w", f.Who, err)
	}
	at, err := f.next(ctx, p, "")
	var last Act // the step before the current one
	restarts := 0
	for range 8 {
		if err != nil {
			return err
		}
		switch {
		case at == "neterror":
			// A password or code cut off may have reached the site:
			// sending it again could count as another attempt. Not a
			// Rejection, so it's tried later rather than refused for good.
			if (last.Sends != "" && !last.Repeatable) || restarts == 2 {
				return fmt.Errorf("%s: the connection dropped after %s", f.Who, sent(last.Sends))
			}
			restarts++
			if err := f.pause(ctx, 3*time.Second); err != nil {
				return err
			}
			if err := p.Navigate(ctx, f.Start); err != nil {
				return fmt.Errorf("%s: %w", f.Who, err)
			}
			last = Act{}
			at, err = f.next(ctx, p, "neterror")
			continue
		case at == Done:
			if f.Take != nil {
				if err := f.Take(ctx, p); err != nil {
					return fmt.Errorf("%s: %w", f.Who, err)
				}
			}
			if jar == nil {
				return nil
			}
			return f.keepCookies(ctx, host, jar)
		case strings.HasPrefix(at, "error:"):
			msg := strings.TrimPrefix(at, "error:")
			if transient(msg) {
				// The page's own request failed (the connection, the
				// proxy): not a refusal, so it's tried again later.
				return fmt.Errorf("%s: %s (after %s)", f.Who, msg, sent(last.Sends))
			}
			if f.Refused != nil {
				return f.Refused(msg, last.Sends)
			}
			return &Rejection{f.Who, msg + " (after " + sent(last.Sends) + ")"}
		}
		act, ok := f.Acts[at]
		if !ok {
			return fmt.Errorf("%s: the page is at an unknown step %q", f.Who, at)
		}
		if err := act.Do(ctx, p); err != nil {
			if IsRejection(err) {
				return err
			}
			return fmt.Errorf("%s: %w", f.Who, err)
		}
		last = act
		at, err = f.next(ctx, p, at)
	}
	return fmt.Errorf("%s: too many steps", f.Who)
}

// next waits for the page to reach a step other than after, passing
// through the Pass ones, bounded as a whole by StepWait.
func (f Flow) next(ctx context.Context, p Page, after string) (string, error) {
	wait := f.StepWait
	if wait == 0 {
		wait = 60 * time.Second
	}
	stepCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	lastAt := ""
	for {
		at, err := p.WaitFor(stepCtx, f.Step, wait)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil && lastAt != "" {
				return "", fmt.Errorf("%s: the page didn't move on from %s (still at %q after %s)", f.Who, cmp.Or(after, "the start"), lastAt, wait)
			}
			return "", fmt.Errorf("%s: the page didn't move on from %s: %w", f.Who, cmp.Or(after, "the start"), err)
		}
		if at != after && !slices.Contains(f.Pass, at) {
			return at, nil
		}
		lastAt = at
		if err := f.pause(stepCtx, 700*time.Millisecond); err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil && lastAt != "" {
				return "", fmt.Errorf("%s: the page didn't move on from %s (still at %q after %s)", f.Who, cmp.Or(after, "the start"), lastAt, wait)
			}
			return "", err
		}
	}
}

// keepCookies copies the Sites' cookies from Chrome into jar, host-only,
// as __Host- cookies are.
func (f Flow) keepCookies(ctx context.Context, host *Host, jar http.CookieJar) error {
	cookies, err := host.cookies(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", f.Who, err)
	}
	for _, ck := range cookies {
		host := strings.TrimPrefix(ck.Domain, ".")
		for _, site := range f.Sites {
			if host == site.Hostname() {
				ck.Domain = ""
				jar.SetCookies(site, []*http.Cookie{ck})
			}
		}
	}
	return nil
}

func (f Flow) pause(ctx context.Context, d time.Duration) error {
	if f.Pause != nil {
		return f.Pause(ctx, d)
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

// transientRE matches a page's message about its own request failing,
// rather than the site refusing what was sent.
var transientRE = regexp.MustCompile(`(?i)failed to fetch|network ?error|networkerror|load failed|timed? ?out|try again (later|in a)|too many requests|service unavailable|something went wrong`)

func transient(msg string) bool { return transientRE.MatchString(msg) }

func sent(what string) string { return cmp.Or(what, "the start") }

// Page is a login page, with what a person does on one.
type Page struct{ *browser.Page }

// Fill clicks a field and types text into it key by key, then checks the
// field holds it: a lost or changed keystroke would read as a wrong
// password.
func (p Page) Fill(ctx context.Context, selector, text string) error {
	if err := p.Click(ctx, selector); err != nil {
		return err
	}
	if err := p.Focus(ctx, selector); err != nil { // in case the click landed on a label
		return err
	}
	if err := p.Type(ctx, text, 70*time.Millisecond); err != nil {
		return err
	}
	var same bool
	q, _ := json.Marshal(selector)
	want, _ := json.Marshal(text)
	if err := p.Eval(ctx, "document.querySelector("+string(q)+").value === "+string(want), &same); err != nil {
		return err
	}
	if !same {
		return fmt.Errorf("typing into %s: the field doesn't hold what was typed", selector)
	}
	return sleep(ctx, 600*time.Millisecond)
}

// Tick ticks a checkbox ("Remember me") if it's there and isn't already,
// and pauses a moment as a person would.
func (p Page) Tick(ctx context.Context, selector string) {
	q, _ := json.Marshal(selector)
	_ = p.Eval(ctx, `(() => { const e = document.querySelector(`+string(q)+`); if (e && !e.checked) e.click() })()`, nil)
	_ = sleep(ctx, 500*time.Millisecond)
}

// Remember ticks every visible checkbox on the page that asks to
// remember the browser ("Remember me", "Keep me signed in", "Trust this
// device", "Don't ask again on this device"), so the site knows it next
// time and later logins skip steps. Other checkboxes are left alone.
func (p Page) Remember(ctx context.Context) {
	_ = p.Eval(ctx, `(() => {
		const want = /remember|keep me (signed|logged) in|stay (signed|logged) in|trust (this|the) (device|browser)|don.t (ask|challenge)/i;
		for (const box of document.querySelectorAll('input[type=checkbox]')) {
			if (box.checked) continue;
			const label = (box.labels && [...box.labels].map(l => l.innerText).join(' ')) || '';
			const near = box.closest('label, .o-form-input, .custom-checkbox, .form-check, div');
			const text = label + ' ' + (box.name || '') + ' ' + (box.id || '') + ' ' + (near ? near.innerText : '');
			if (!want.test(text)) continue;
			const target = box.offsetParent !== null ? box : (box.labels && box.labels[0]);
			if (target && target.offsetParent !== null) target.click();
		}
	})()`, nil)
	_ = sleep(ctx, 400*time.Millisecond)
}

// Settle waits for the page to finish loading, then a little longer, as
// a person takes the page in before typing.
func (p Page) Settle(ctx context.Context, extra time.Duration) error {
	_, _ = p.WaitFor(ctx, "document.readyState === 'complete' && 'ready'", 20*time.Second)
	return sleep(ctx, extra)
}

func sleep(ctx context.Context, d time.Duration) error {
	return Flow{}.pause(ctx, d)
}
