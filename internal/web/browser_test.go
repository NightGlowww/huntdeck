package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// fakeBrowser is the shared login browser, with a tab for "fake": Show
// opens it, and shown lists the tabs brought forward.
type fakeBrowser struct {
	running  bool
	shown    []string
	showErr  error
	closeErr error
}

func (b *fakeBrowser) Show(_ context.Context, tab string) error {
	if b.showErr != nil {
		return b.showErr
	}
	b.running = true
	b.shown = append(b.shown, tab)
	return nil
}

func (b *fakeBrowser) Tabs() []string { return []string{"fake"} }

func (b *fakeBrowser) Close() error {
	if b.closeErr != nil {
		return b.closeErr
	}
	b.running = false
	return nil
}

func (b *fakeBrowser) Running() bool { return b.running }

func TestBrowserTab(t *testing.T) {
	// Without a login browser there's no tab, and ?tab=browser shows Polling.
	plain := newTestEnv(t)
	_, body := plain.do(plain.client(), "GET", "/settings?tab=browser", nil, nil)
	if strings.Contains(body, `href="/settings?tab=browser"`) || !strings.Contains(body, `id="platforms"`) {
		t.Error("the Browser tab shows without a login browser")
	}

	b := &fakeBrowser{}
	e := newTestEnv(t, func(d *Deps) {
		d.Browser = b
		d.VNC = &VNC{Addr: "127.0.0.1:1", Dir: t.TempDir()}
	})
	c := e.client()
	_, body = e.do(c, "GET", "/settings?tab=browser", nil, nil)
	for _, want := range []string{`id="browser"`, `action="/settings/browser/show"`, `name="platform" value="fake"`,
		`data-browser-state`, `data-vnc-toggle`, `data-vnc-wrap hidden`, `data-vnc="/novnc/websockify"`,
		`data-status-url="/settings/browser/status"`, `data-msg-closed=`, `data-msg-failed=`,
		`data-vnc-retry`, `Watch screen`, `Hide screen`, "vncview.js", "Close browser"} {
		if !strings.Contains(body, want) {
			t.Errorf("Browser tab lacks %s", want)
		}
	}
	res, statusBody := e.do(c, "GET", "/settings/browser/status", nil, nil)
	var st browserReply
	if res.StatusCode != http.StatusOK || json.Unmarshal([]byte(statusBody), &st) != nil || st.Running {
		t.Errorf("browser status before open = %d %q", res.StatusCode, statusBody)
	}
	if strings.Contains(body, "data-running") {
		t.Error("the screen says the browser is running before it is")
	}

	tests := []struct {
		name       string
		path       string
		form       url.Values
		json       bool
		showErr    error
		closeErr   error
		wantStatus int
		wantTo     string // the redirect, without a script
		wantErr    string // the reply's error, with one
		wantShown  int
	}{
		{"show, from a form", "/settings/browser/show", url.Values{"platform": {"fake"}}, false, nil, nil, http.StatusSeeOther, "/settings?tab=browser", "", 1},
		{"show, from the script", "/settings/browser/show", url.Values{"platform": {"fake"}}, true, nil, nil, http.StatusOK, "", "", 2},
		{"an unknown platform", "/settings/browser/show", url.Values{"platform": {"nope"}}, true, nil, nil, http.StatusNotFound, "", "doesn't log in with the browser", 2},
		{"Chrome won't start", "/settings/browser/show", url.Values{"platform": {"fake"}}, false, errors.New("no Chrome"), nil, http.StatusSeeOther, "/settings?tab=browser&err=browser-failed", "", 2},
		{"close during a login", "/settings/browser/close", url.Values{}, true, nil, weblogin.ErrLoginRunning, http.StatusConflict, "", "login is running", 2},
		{"close", "/settings/browser/close", url.Values{}, true, nil, nil, http.StatusOK, "", "", 2},
	}
	for _, tt := range tests {
		b.showErr, b.closeErr = tt.showErr, tt.closeErr
		var h http.Header
		if tt.json {
			h = http.Header{"Accept": {"application/json"}}
		}
		res, body := e.do(c, "POST", tt.path, tt.form, h)
		if res.StatusCode != tt.wantStatus || res.Header.Get("Location") != tt.wantTo {
			t.Errorf("%s: %d → %q, want %d → %q", tt.name, res.StatusCode, res.Header.Get("Location"), tt.wantStatus, tt.wantTo)
		}
		if tt.json {
			var reply browserReply
			if err := json.Unmarshal([]byte(body), &reply); err != nil || !strings.Contains(reply.Error, tt.wantErr) || (tt.wantErr == "") != (reply.Error == "") {
				t.Errorf("%s: reply %s, want error %q", tt.name, body, tt.wantErr)
			}
		}
		if len(b.shown) != tt.wantShown {
			t.Errorf("%s: shown %v", tt.name, b.shown)
		}
	}
	if b.shown[0] != "fake" || b.running {
		t.Errorf("shown %v, running %v; want fake's tab, then closed", b.shown, b.running)
	}

	// A GET can't open it: state changes are POSTs.
	if res, _ := e.do(c, "GET", "/settings/browser/show?platform=fake", nil, nil); res.StatusCode < 400 || len(b.shown) != 2 {
		t.Errorf("GET show: %d, shown %v; want it refused", res.StatusCode, b.shown)
	}

	// Without VNC, live view is absent and the note explains why.
	withoutVNC := newTestEnv(t, func(d *Deps) { d.Browser = &fakeBrowser{} })
	_, noVNCBody := withoutVNC.do(withoutVNC.client(), "GET", "/settings?tab=browser", nil, nil)
	if strings.Contains(noVNCBody, `data-vnc`) || !strings.Contains(noVNCBody, `HUNTDECK_VNC=false`) {
		t.Errorf("expected non-live note without VNC")
	}
	// Read-only copies never expose browser status.
	ro := newTestEnv(t, func(d *Deps) { d.ReadOnly = true; d.Browser = &fakeBrowser{} })
	res, _ = ro.do(ro.client(), "GET", "/settings/browser/status", nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("readOnly /settings/browser/status = %d, want 404", res.StatusCode)
	}
}
