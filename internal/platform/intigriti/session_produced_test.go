package intigriti

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
)

// sessionProduced decides whether a login attempt is kept or undone. Getting it
// backwards would be a silent disaster either way: a good session rolled back,
// or a failed attempt kept because some unrelated cookie was set.
func TestSessionProduced(t *testing.T) {
	u, _ := url.Parse("https://app.intigriti.com")
	cases := []struct {
		name    string
		cookies []*http.Cookie
		want    bool
	}{
		{"no cookies at all", nil, false},
		{"only a stranger's crumbs", []*http.Cookie{{Name: ".AspNetCore.Antiforgery.x", Value: "c", Path: "/"}}, false},
		{"the session cookie, empty", []*http.Cookie{{Name: appSessionCookie, Value: "", Path: "/"}}, false},
		{"the session cookie, live", []*http.Cookie{{Name: appSessionCookie, Value: "v", Path: "/"}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			jar, _ := cookiejar.New(nil)
			if c.cookies != nil {
				jar.SetCookies(u, c.cookies)
			}
			if got := sessionProduced(jar, u); got != c.want {
				t.Fatalf("sessionProduced = %v, want %v", got, c.want)
			}
		})
	}
}
