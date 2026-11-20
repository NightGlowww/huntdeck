package sessionjar

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// The guarantee: an attempt that damages the session can be undone completely -
// memory and the file on disk, not just the cookies one layer can see.
func TestRollbackUndoesDamageToMemoryAndFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	jar, err := Open(path, "app.example.com")
	if err != nil {
		t.Fatal(err)
	}
	u := mustURL(t, "https://app.example.com/")
	jar.SetCookies(u, []*http.Cookie{{Name: "__Host-session", Value: "live", Path: "/", Secure: true}})
	before := jar.Checkpoint()

	// What a refused login does: deletes the session and leaves its own crumbs.
	jar.SetCookies(u, []*http.Cookie{{Name: "__Host-session", Path: "/", MaxAge: -1}})
	jar.SetCookies(u, []*http.Cookie{{Name: "crumb", Value: "x", Path: "/"}})
	if err := jar.Rollback(before); err != nil {
		t.Fatal(err)
	}

	var session, crumbs string
	for _, c := range jar.Cookies(u) {
		switch c.Name {
		case "__Host-session":
			session = c.Value
		case "crumb":
			crumbs = c.Value
		}
	}
	if session != "live" {
		t.Fatalf("rollback lost the session in memory: %q", session)
	}
	if crumbs != "" {
		t.Fatalf("rollback kept the crumbs: %q", crumbs)
	}

	// And a jar opened from the file afterwards has the same state: the rollback
	// reached the disk, not only the process.
	reopened, err := Open(path, "app.example.com")
	if err != nil {
		t.Fatal(err)
	}
	var onDisk string
	for _, c := range reopened.Cookies(u) {
		if c.Name == "__Host-session" {
			onDisk = c.Value
		}
	}
	if onDisk != "live" {
		t.Fatalf("the file kept the damage: %q", onDisk)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no file written: %v", err)
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
