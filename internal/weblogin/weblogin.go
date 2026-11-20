// Package weblogin signs in to platform websites as a person would, in a
// windowed Chrome (package browser), for the data their APIs leave out. It
// holds what HackerOne's and Intigriti's website logins share:
//
//   - Flow walks a sign-in page by page: the email, the password, a TOTP
//     code, a bot check waited out, typed key by key and checked.
//   - History is each login's record across restarts: a refused password
//     sticks until a restart, and a failed browser login blocks the next
//     one for RetryAfter, so a broken login doesn't open Chrome every poll.
//   - Account answers TOTP codes, each only once.
//
// The platforms keep their sessions themselves: what proves one, how it's
// renewed without a password, and what it's used for.
package weblogin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/browser"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/totp"
)

// RetryAfter is how long a failed browser login blocks the next one.
const RetryAfter = 30 * time.Minute

// LoginBudget is how long one browser login may take: several page
// steps, each waiting for the page to move, plus a slow proxy.
const LoginBudget = 4 * time.Minute

// ErrWaiting is wrapped by History.Login's error when the last login
// failed under RetryAfter ago, so none was tried.
var ErrWaiting = errors.New("retrying after")

// Chrome is how a platform's login browser starts.
type Chrome struct {
	Path     string   // the executable; found on PATH if empty
	Headless bool     // without a window (by default it gets one, on a virtual display if need be)
	Proxy    *url.URL // an HTTP proxy, which may carry credentials
	TimeZone string   // its clock, to match the proxy's country
	Profile  string   // a profile directory kept between runs
	Display  string   // the X display to show it on; see browser.Options
}

// Start starts it.
func (c Chrome) Start(ctx context.Context) (*browser.Browser, error) {
	path, err := browser.Find(c.Path)
	if err != nil {
		return nil, err
	}
	return browser.Start(ctx, browser.Options{Path: path, Headless: c.Headless, Proxy: c.Proxy,
		TimeZone: c.TimeZone, Profile: c.Profile, Display: c.Display})
}

// Rejection is the platform refusing the user's credentials. History
// keeps it until a restart, so a wrong password isn't tried every poll
// until the platform locks the account.
type Rejection struct {
	Who string // what was refused: "hackerone: website login"
	Msg string
}

func (e *Rejection) Error() string { return e.Who + ": " + e.Msg }

// IsRejection reports whether err is, or wraps, a Rejection.
func IsRejection(err error) bool {
	var r *Rejection
	return errors.As(err, &r)
}

// Account is who signs in.
type Account struct {
	Who             string // starts its errors: "intigriti: login"
	Email, Password string
	TOTPSecret      string // with 2FA
	TOTPEnv         string // the setting TOTPSecret comes from, for errors
	Now             func() time.Time

	mu       sync.Mutex
	lastStep int64 // TOTP step last used
}

// Code returns the current TOTP code, waiting for the next one if this
// one was already used (a code works once).
func (a *Account) Code(ctx context.Context) (string, error) {
	if a.TOTPSecret == "" {
		return "", &Rejection{a.Who, "the account asks for a 2FA code; set " + a.TOTPEnv}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if totp.Step(now) == a.lastStep {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(totp.NextStep(now).Sub(now) + time.Second):
		}
		now = a.now()
	}
	code, err := totp.Code(a.TOTPSecret, now)
	if err != nil {
		return "", &Rejection{a.Who, a.TOTPEnv + ": " + err.Error()}
	}
	a.lastStep = totp.Step(now)
	return code, nil
}

func (a *Account) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// History is a website login's record, for Settings, kept in a file (if
// it has a path) across restarts. Its methods are safe to call at once.
type History struct {
	path string
	// Now is the clock; tests set it.
	Now func() time.Time

	mu          sync.Mutex
	stuck       error // a Rejection: kept until restart
	failure     error
	lastFailure time.Time // of a browser login
	lastBrowser time.Time // last browser login that worked
	lastRenewal time.Time // last session renewed without the browser
}

// state is what the file holds.
type state struct {
	LastFailure time.Time `json:"last_failure,omitzero"`
	Error       string    `json:"error,omitempty"`
	LastBrowser time.Time `json:"last_browser_login,omitzero"`
	LastRenewal time.Time `json:"last_renewal,omitzero"`
}

// NewHistory returns the history kept at path ("": not kept), picking up
// a failure from before a restart so the wait before the next try still
// applies. A refused password isn't kept as such: restarting is how a
// corrected one is put in.
func NewHistory(path string) *History {
	h := &History{path: path, Now: time.Now}
	raw, err := os.ReadFile(path)
	if path == "" || err != nil {
		return h
	}
	var st state
	if json.Unmarshal(raw, &st) != nil {
		return h
	}
	h.lastBrowser, h.lastRenewal = st.LastBrowser, st.LastRenewal
	if !st.LastFailure.IsZero() && st.Error != "" {
		h.failure, h.lastFailure = errors.New(st.Error), st.LastFailure
	}
	return h
}

// Stuck is the rejection that stops all logins until a restart, or nil.
func (h *History) Stuck() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stuck
}

// Login runs a browser login unless a rejection sticks or the last one
// failed less than RetryAfter ago, and records how it went.
func (h *History) Login(ctx context.Context, login func(context.Context) error) error {
	h.mu.Lock()
	stuck, failure, lastFailure := h.stuck, h.failure, h.lastFailure
	h.mu.Unlock()
	if stuck != nil {
		return stuck
	}
	if failure != nil && h.Now().Sub(lastFailure) < RetryAfter {
		return fmt.Errorf("%w (%w %s)", failure, ErrWaiting, lastFailure.Add(RetryAfter).Format(time.Kitchen))
	}
	// A login runs longer than the poll that triggered it, and a poll can be
	// canceled at any moment (a restart, a deadline, another platform giving up).
	// Cancelling a browser login halfway leaves a filled-in page nobody submits:
	// that is how "the page didn't move on from email: context canceled" is
	// produced, and it is not the platform refusing anything. The work is worth
	// finishing, so it gets its own budget and does not inherit the caller's
	// cancellation. Values still pass through.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), LoginBudget)
	defer cancel()
	if err := login(ctx); err != nil {
		h.record(func() {
			if IsRejection(err) {
				h.stuck = err
			}
			h.failure, h.lastFailure = err, h.Now()
		})
		return err
	}
	h.record(func() { h.failure, h.lastBrowser = nil, h.Now() })
	return nil
}

// Renewed records a session renewed without the browser.
func (h *History) Renewed() { h.record(func() { h.lastRenewal = h.Now() }) }

// Report is the login for Settings; signedIn says whether a session is
// held now.
func (h *History) Report(signedIn bool) platform.WebSession {
	at := func(t time.Time) *time.Time {
		if t.IsZero() {
			return nil
		}
		t = t.UTC()
		return &t
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	ws := platform.WebSession{
		SignedIn:         signedIn && h.stuck == nil,
		LastBrowserLogin: at(h.lastBrowser),
		LastRenewal:      at(h.lastRenewal),
		Stuck:            h.stuck != nil,
	}
	if h.failure != nil {
		ws.Failure, ws.LastFailure = h.failure.Error(), at(h.lastFailure)
		if next := h.lastFailure.Add(RetryAfter); h.stuck == nil && next.After(h.Now()) {
			ws.RetryAfter = at(next)
		}
	}
	return ws
}

// record changes the history and saves it. Best effort: a failed write
// only loses the wait after a failure on a restart.
func (h *History) record(change func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	change()
	if h.path == "" {
		return
	}
	st := state{LastBrowser: h.lastBrowser, LastRenewal: h.lastRenewal}
	if h.failure != nil {
		st.LastFailure, st.Error = h.lastFailure, h.failure.Error()
	}
	if raw, err := json.Marshal(st); err == nil && os.MkdirAll(filepath.Dir(h.path), 0o700) == nil {
		_ = os.WriteFile(h.path, raw, 0o600)
	}
}

// Waiting reports whether a login would not even be attempted: the last one
// failed under RetryAfter ago, or a rejection is stuck until a restart. A caller
// about to drop a session so it can sign in again has to ask this first -
// dropping it is only worth doing when a login will actually run.
func (h *History) Waiting() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stuck != nil {
		return true
	}
	return h.failure != nil && h.Now().Sub(h.lastFailure) < RetryAfter
}
