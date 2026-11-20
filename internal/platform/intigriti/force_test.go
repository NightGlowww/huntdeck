package intigriti

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
)

// force drops the cookie before trying a new login, so the snapshot has to be
// taken first: otherwise a forced attempt that fails has nothing to restore and
// the session is gone for good.
func TestSnapshotTakenBeforeTheForcedDropStillRestores(t *testing.T) {
	u, _ := url.Parse("https://app.intigriti.com")
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(u, []*http.Cookie{{Name: "__Host-Intigriti.Web.Researcher", Value: "live", Path: "/", Secure: true}})

	kept := takeSnapshot(jar, u) // what ensureSession does before force
	jar.SetCookies(u, []*http.Cookie{{Name: "__Host-Intigriti.Web.Researcher", Path: "/", MaxAge: -1}})
	kept.restore(jar)

	var session string
	for _, c := range jar.Cookies(u) {
		if c.Name == "__Host-Intigriti.Web.Researcher" {
			session = c.Value
		}
	}
	if session != "live" {
		t.Fatalf("a forced login that failed lost the session: %q", session)
	}
}
