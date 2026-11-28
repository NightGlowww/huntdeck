package weblogin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/browser"
)

// Host is the one Chrome every website login shares, as a person has one
// browser with a tab per site: one profile, holding every platform's
// remembered login, on the login display (see browser.Shared) where it can
// be watched and used. It starts when first needed and then stays open;
// if it exits (its window closed on the display), the next use starts it
// again.
type Host struct {
	chrome Chrome

	mu    sync.Mutex
	sites map[string]site // each platform's tab, by name
	order []string        // their names, as registered
	b     *browser.Browser
	tabs  map[string]*browser.Page // each platform's, by name

	// login lets one login run at a time: it has the window's focus.
	login sync.Mutex
}

// NewHost returns the shared browser, not yet started.
func NewHost(c Chrome) *Host { return &Host{chrome: c, sites: map[string]site{}} }

// Session is where a platform keeps its website session
// (sessionjar.Jar): the cookies HuntDeck itself uses.
type Session interface {
	Hosts() []string
	Saved(host string) []*http.Cookie
}

// StorageSession is a Session a web app also keeps in its origin's
// localStorage (YesWeHack's token): Show puts the items there too.
type StorageSession interface {
	Session
	// LocalStorage is the origin ("https://example.com") and its items;
	// none while there's no session.
	LocalStorage() (origin string, items map[string]string)
}

// site is a platform's tab: the page it opens at, and its session.
type site struct {
	home    string
	session Session
}

// Register names a platform's tab, for Show: the page it opens at, and
// the session it shows.
func (h *Host) Register(name, home string, session Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.sites[name]; !ok {
		h.order = append(h.order, name)
	}
	h.sites[name] = site{home: home, session: session}
}

// Tabs are the registered platforms' names.
func (h *Host) Tabs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.order)
}

// Chrome is how the browser starts.
func (h *Host) Chrome() Chrome { return h.chrome }

// ErrLoginRunning means a login is using the browser just now.
var ErrLoginRunning = errors.New("a login is running in the browser")

// Running reports whether the browser is open.
func (h *Host) Running() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.alive()
}

// Display returns the X display of the currently running Chrome, or "" if closed.
func (h *Host) Display() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.alive() {
		return ""
	}
	return h.b.Display()
}

// alive reports whether b is still running, forgetting it if not. The
// caller holds mu.
func (h *Host) alive() bool {
	if h.b == nil {
		return false
	}
	select {
	case <-h.b.Exited():
		_ = h.b.Close()
		h.b, h.tabs = nil, nil
		return false
	default:
		return true
	}
}

// Tab returns the named tab, starting the browser if it isn't running,
// and opening the tab (or taking a blank one nobody has) if it's gone.
// fresh says it's new, still blank.
func (h *Host) Tab(ctx context.Context, name string) (p *browser.Page, fresh bool, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.alive() {
		b, err := h.chrome.Start(ctx)
		if err != nil {
			return nil, false, err
		}
		h.b, h.tabs = b, map[string]*browser.Page{}
	}
	open, err := h.b.Tabs(ctx)
	if err != nil {
		return nil, false, err
	}
	if p := h.tabs[name]; p != nil && slices.ContainsFunc(open, func(t browser.Tab) bool { return t.ID == p.ID() }) {
		return p, false, nil
	}
	for _, t := range open {
		if t.URL == "about:blank" && !h.taken(t.ID) {
			if p, err = h.b.AttachPage(ctx, t.ID); err != nil {
				return nil, false, err
			}
			h.tabs[name] = p
			return p, true, nil
		}
	}
	if p, err = h.b.NewPage(ctx); err != nil {
		return nil, false, err
	}
	h.tabs[name] = p
	return p, true, nil
}

func (h *Host) taken(id string) bool {
	for _, p := range h.tabs {
		if p.ID() == id {
			return true
		}
	}
	return false
}

// Show brings a registered platform's tab to the front, starting the
// browser if need be. The browser first takes up the session HuntDeck
// uses (the profile's own may be older), so the tab shows it; a new tab
// opens at the platform's page.
func (h *Host) Show(ctx context.Context, name string) error {
	h.mu.Lock()
	s, ok := h.sites[name]
	h.mu.Unlock()
	if !ok {
		return fmt.Errorf("no tab for %s", name)
	}
	p, fresh, err := h.Tab(ctx, name)
	if err != nil {
		return err
	}
	if s.session != nil {
		h.mu.Lock()
		b := h.b
		h.mu.Unlock()
		for _, host := range s.session.Hosts() {
			if err := b.SetCookies(ctx, host, s.session.Saved(host)); err != nil {
				return fmt.Errorf("giving the browser %s's session: %w", name, err)
			}
		}
	}
	if ss, ok := s.session.(StorageSession); ok {
		if origin, items := ss.LocalStorage(); origin != "" && len(items) > 0 {
			// Set on a page of the origin, then open the app with them.
			if err := p.Navigate(ctx, origin+"/robots.txt"); err != nil {
				return err
			}
			if err := setStorage(ctx, p, items); err != nil {
				return fmt.Errorf("giving the browser %s's session: %w", name, err)
			}
			fresh = true
		}
	}
	if fresh && s.home != "" {
		if err := p.Navigate(ctx, s.home); err != nil {
			return err
		}
	}
	return p.Activate(ctx)
}

// setStorage sets items in the page's localStorage, once the page has its
// origin (a navigation may still be under way).
func setStorage(ctx context.Context, p *browser.Page, items map[string]string) error {
	js, err := json.Marshal(items)
	if err != nil {
		return err
	}
	expr := `(() => { if (location.origin === 'null' || document.readyState === 'loading') return '';
		const items = ` + string(js) + `; for (const k in items) localStorage.setItem(k, items[k]); return 'set' })()`
	_, err = p.WaitFor(ctx, expr, 30*time.Second)
	return err
}

// Close ends the browser, unless a login is running in it.
func (h *Host) Close() error {
	if !h.login.TryLock() {
		return ErrLoginRunning
	}
	defer h.login.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.b == nil {
		return nil
	}
	err := h.b.Close()
	h.b, h.tabs = nil, nil
	return err
}

// ClearCookies expires all cookies matching domainSuffix and names in the
// running browser, without touching other domains.
func (h *Host) ClearCookies(ctx context.Context, domainSuffix string, names []string) error {
	return h.ClearCookieValue(ctx, domainSuffix, names, "")
}

// ClearCookieValue expires matching cookies in the running browser (optionally
// restricted to cookies holding onlyValue).
func (h *Host) ClearCookieValue(ctx context.Context, domainSuffix string, names []string, onlyValue string) error {
	h.mu.Lock()
	var b *browser.Browser
	if h.alive() {
		b = h.b
	}
	h.mu.Unlock()
	if b == nil {
		return nil
	}
	return b.DeleteMatchingCookies(ctx, domainSuffix, names, onlyValue)
}

// cookies are every cookie the browser holds.
func (h *Host) cookies(ctx context.Context) ([]*http.Cookie, error) {
	h.mu.Lock()
	b := h.b
	h.mu.Unlock()
	if b == nil {
		return nil, errors.New("the browser closed")
	}
	return b.Cookies(ctx)
}
