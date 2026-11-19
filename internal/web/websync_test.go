package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// The settings page has a browser sync panel apart from the polls: its
// switch and interval save, reset, and it can be run now.
func TestBrowserSyncSettings(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	_, body := e.do(c, http.MethodGet, "/settings", nil, nil)
	for _, want := range []string{`id="websync-`, `<input type="hidden" name="web" value="fake">`, `name="web_interval_fake" value="1d" form="sched"`,
		`<span class="sn">Website login</span>`, "signed in", `title="Reads for each program: Logged-in stats`, "uses the website login, every", `name="mode" value="web"`,
		"<span class=\"zero\">never</span>", `<span class="sn">API token</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page lacks %q", want)
		}
	}

	for _, tt := range []struct {
		name     string
		form     url.Values
		wantCode int
		wantBody string
		want     store.WebSyncSettings
	}{
		{"save", url.Values{"platform": {"fake"}, "enabled": {"1"}, "interval": {"12h"}},
			http.StatusSeeOther, "", store.WebSyncSettings{Enabled: true, Interval: 12 * time.Hour}},
		{"off", url.Values{"platform": {"fake"}, "interval": {"off"}},
			http.StatusSeeOther, "", store.WebSyncSettings{Interval: 12 * time.Hour}},
		{"too often", url.Values{"platform": {"fake"}, "enabled": {"1"}, "interval": {"1h"}},
			http.StatusBadRequest, "between 6h and 7 days", store.WebSyncSettings{Interval: 12 * time.Hour}},
		{"unknown platform", url.Values{"platform": {"nope"}, "enabled": {"1"}, "interval": {"1d"}},
			http.StatusNotFound, "no browser sync", store.WebSyncSettings{Interval: 12 * time.Hour}},
	} {
		res, body := e.do(c, http.MethodPost, "/settings/websync", tt.form, nil)
		if res.StatusCode != tt.wantCode || !strings.Contains(body, tt.wantBody) {
			t.Errorf("%s: status %d, want %d with %q", tt.name, res.StatusCode, tt.wantCode, tt.wantBody)
		}
		if got := e.sched.Web("fake"); got != tt.want {
			t.Errorf("%s: settings = %+v, want %+v", tt.name, got, tt.want)
		}
	}

	// Save intervals saves the browser sync in the platform's row too.
	sched := func(web url.Values) url.Values {
		v := url.Values{"platform": {"fake"}, "enabled_fake": {"1"}, "quick_fake": {"15m"}, "full_fake": {"6h"}, "web": {"fake"}}
		for k, x := range web {
			v[k] = x
		}
		return v
	}
	for _, tt := range []struct {
		name     string
		form     url.Values
		wantCode int
		want     store.WebSyncSettings
	}{
		{"with the polls", sched(url.Values{"web_enabled_fake": {"1"}, "web_interval_fake": {"2d"}}), http.StatusSeeOther,
			store.WebSyncSettings{Enabled: true, Interval: 48 * time.Hour}},
		{"unticked", sched(url.Values{"web_interval_fake": {"2d"}}), http.StatusSeeOther, store.WebSyncSettings{Interval: 48 * time.Hour}},
		{"too often", sched(url.Values{"web_enabled_fake": {"1"}, "web_interval_fake": {"2h"}}), http.StatusBadRequest,
			store.WebSyncSettings{Interval: 48 * time.Hour}},
		{"garbled", sched(url.Values{"web_enabled_fake": {"1"}, "web_interval_fake": {"soon"}}), http.StatusBadRequest,
			store.WebSyncSettings{Interval: 48 * time.Hour}},
	} {
		res, body := e.do(c, http.MethodPost, "/settings/platform", tt.form, nil)
		if res.StatusCode != tt.wantCode {
			t.Errorf("%s: status %d, want %d: %.200s", tt.name, res.StatusCode, tt.wantCode, body)
		}
		if got := e.sched.Web("fake"); got != tt.want {
			t.Errorf("%s: settings = %+v, want %+v", tt.name, got, tt.want)
		}
	}

	res, _ := e.do(c, http.MethodPost, "/settings/websync/reset", url.Values{"platform": {"fake"}}, nil)
	if res.StatusCode != http.StatusSeeOther || e.sched.Web("fake") != store.DefaultWebSync || e.sched.WebOverridden("fake") {
		t.Errorf("reset: %d, %+v", res.StatusCode, e.sched.Web("fake"))
	}

	e.do(c, http.MethodPost, "/settings/poll", url.Values{"platform": {"fake"}, "mode": {"web"}}, nil)
	if got := strings.Join(e.poller.calls, ","); got != "fake/web" {
		t.Errorf("poller calls = %s", got)
	}
}
