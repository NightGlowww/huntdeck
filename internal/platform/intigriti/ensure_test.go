package intigriti

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/sessionjar"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// The end-to-end version of the guarantee, at the level that decides it:
// ensureSession holds a live session, a forced refresh is refused, and the jar
// on disk must be byte-identical afterwards. This is the case that damaged a
// working session on the live instance - three writes landed while the attempt
// was being judged, and the cookie-level restore raced them.
func TestForcedRefreshThatFailsLeavesTheJarByteIdentical(t *testing.T) {
	path := filepath.Join(t.TempDir(), "intigriti.json")
	jar, err := sessionjar.Open(path, "app.intigriti.com")
	if err != nil {
		t.Fatal(err)
	}
	app, _ := url.Parse("https://app.intigriti.com")
	jar.SetCookies(app, []*http.Cookie{{Name: appSessionCookie, Value: "live", Path: "/", Secure: true}})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	s := &webSession{
		jar:     jar,
		app:     app,
		history: &weblogin.History{Now: time.Now},
		login: func(context.Context) error {
			// What the platform and the browser do to the jar while a login is
			// refused: clear the cookie, then leave their own crumbs.
			jar.SetCookies(app, []*http.Cookie{{Name: appSessionCookie, Path: "/", MaxAge: -1}})
			jar.SetCookies(app, []*http.Cookie{{Name: ".AspNetCore.Antiforgery.x", Value: "crumb", Path: "/"}})
			return errors.New("intigriti: login: bot-like behavior detected")
		},
	}
	base, _ := url.Parse("http://127.0.0.1:9/external/researcher")
	c := &Client{web: s, base: base, http: httpclient.New(httpclient.Options{})}

	// force: the app refused the session, which is the path that deletes it
	if err := c.ensureSession(context.Background(), true); err == nil {
		t.Fatal("the forced refresh should have failed")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("the refused login rewrote the session file:\n before: %s\n after:  %s", before, after)
	}
	if got := s.appCookie(appSessionCookie); got != "live" {
		t.Fatalf("the live session is gone from memory: %q", got)
	}
}
