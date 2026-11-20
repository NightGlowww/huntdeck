package intigriti

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
)

// The outage this pins: Intigriti answers a bot-flagged login by clearing the
// session cookie, and that cookie was a live browser session another way into
// the app. A refused login must leave the jar as it found it.
func TestFailedLoginLeavesTheSessionAlone(t *testing.T) {
	u, _ := url.Parse("https://app.intigriti.com")
	jar, _ := cookiejar.New(nil)
	good := &http.Cookie{Name: "__Host-Intigriti.Web.Researcher", Value: "live-session", Path: "/", Secure: true}
	jar.SetCookies(u, []*http.Cookie{good})

	kept := takeSnapshot(jar, u)
	// What the platform does to a refused attempt: deletes it, and leaves
	// its own pre-login crumbs behind.
	jar.SetCookies(u, []*http.Cookie{{Name: "__Host-Intigriti.Web.Researcher", Path: "/", MaxAge: -1}})
	jar.SetCookies(u, []*http.Cookie{{Name: ".AspNetCore.Antiforgery.x", Value: "crumb", Path: "/"}})
	kept.restore(jar)

	var session, crumbs string
	for _, c := range jar.Cookies(u) {
		switch c.Name {
		case "__Host-Intigriti.Web.Researcher":
			session = c.Value
		case ".AspNetCore.Antiforgery.x":
			crumbs = c.Value
		}
	}
	if session != "live-session" {
		t.Fatalf("the refused login took the live session: %q", session)
	}
	if crumbs != "" {
		t.Fatalf("the refused login left its crumbs behind: %q", crumbs)
	}
}

// A snapshot of an empty jar restores an empty jar: nothing invented.
func TestSnapshotOfNothingRestoresNothing(t *testing.T) {
	u, _ := url.Parse("https://app.intigriti.com")
	jar, _ := cookiejar.New(nil)
	kept := takeSnapshot(jar, u)
	jar.SetCookies(u, []*http.Cookie{{Name: "added", Value: "x", Path: "/"}})
	kept.restore(jar)
	if got := jar.Cookies(u); len(got) != 0 {
		t.Fatalf("restore invented %d cookie(s)", len(got))
	}
}
