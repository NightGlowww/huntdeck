package intigriti

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// The loss this pins: a login was refused at 09:05, the quick poll at 09:19
// asked for a forced refresh, the session was dropped so a new login could run,
// and the login was never attempted - the retry window had not passed. The
// session was gone for nothing.
func TestForcedRefreshKeepsTheSessionWhileALoginIsOnHold(t *testing.T) {
	app, _ := url.Parse("https://app.intigriti.com")
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(app, []*http.Cookie{{Name: appSessionCookie, Value: "live", Path: "/"}})

	h := &weblogin.History{Now: time.Now}
	// A login that was refused a minute ago: the window has not passed.
	_ = h.Login(context.Background(), func(context.Context) error {
		return errors.New("intigriti: login: bot-like behavior detected")
	})

	c := &Client{web: &webSession{jar: jar, app: app, history: h,
		login: func(context.Context) error { t.Fatal("a login ran while it was on hold"); return nil }}}

	if err := c.ensureSession(context.Background(), true); err == nil {
		t.Fatal("a forced refresh reported success")
	}

	var session string
	for _, ck := range jar.Cookies(app) {
		if ck.Name == appSessionCookie {
			session = ck.Value
		}
	}
	if session != "live" {
		t.Fatalf("the session was dropped while no login could run: %q", session)
	}
	_ = httpclient.Options{}
}
