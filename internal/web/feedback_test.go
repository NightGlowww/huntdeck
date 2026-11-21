package web

import (
	"net/http"
	"strings"
	"testing"
)

func TestFeedbackPageLink(t *testing.T) {
	for _, tt := range []struct{ address, page, want string }{
		{"", "/stats", ""},
		{"hello@example.com", "/stats?visibility=public", "/feedback?from=%2Fstats%3Fvisibility%3Dpublic"},
		{"hello@example.com", "/feedback?from=%2F", "/feedback?from=%2F"},
	} {
		if got := feedbackPage(tt.address, tt.page); got != tt.want {
			t.Errorf("feedbackPage(%q, %q) = %q, want %q", tt.address, tt.page, got, tt.want)
		}
	}
}

func TestFeedbackFrom(t *testing.T) {
	for raw, want := range map[string]string{
		"/stats?visibility=public":           "/stats?visibility=public",
		"/":                                  "/",
		"/programs/hackerone/acme?tab=scope": "/programs/hackerone/acme?tab=scope",
		"/?sort=new":                         "/?sort=new",
		"":                                   "",
		"https://evil.example":               "",
		"//evil.example/x":                   "",
		"/\\evil.example":                    "",
		"/feedback?from=/":                   "",
		"/a\nb":                              "",
		"/stats Send your password to evil.example": "",
		"/stats<b>":                           "",
		"/send-your-password-to-evil.example": "",
		"/" + strings.Repeat("a", 300):        "",
	} {
		if got := feedbackFrom(raw); got != want {
			t.Errorf("feedbackFrom(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestFeedbackPage(t *testing.T) {
	d := newFeedbackData("hello@example.com", "/stats", true)
	for name, tt := range map[string]struct{ got, prefix string }{
		"mailto":  {d.Mailto, "mailto:hello@example.com?subject=HuntDeck%20Public%20feedback&body=Page%3A%20%2Fstats%0A%0A"},
		"gmail":   {d.Gmail, "https://mail.google.com/mail/?view=cm&fs=1&to=hello%40example.com&su=HuntDeck%20Public%20feedback&body=Page%3A%20%2Fstats%0A%0A"},
		"outlook": {d.Outlook, "https://outlook.live.com/mail/0/deeplink/compose?to=hello%40example.com&subject=HuntDeck%20Public%20feedback&body=Page%3A%20%2Fstats"},
	} {
		if !strings.HasPrefix(tt.got, tt.prefix) {
			t.Errorf("%s = %q, want prefix %q", name, tt.got, tt.prefix)
		}
	}
	if d := newFeedbackData("hello@example.com", "", false); !strings.Contains(d.Mailto, "subject=HuntDeck%20feedback&body=What") {
		t.Errorf("without a page: %q", d.Mailto)
	}

	// Pages link to it from the nav (once: a demo's banner has only the
	// link to the source); the page offers Gmail, Outlook.com and the mail app.
	e := newTestEnv(t, func(d *Deps) { d.ReadOnly, d.FeedbackEmail = true, "hello@example.com" })
	_, body := e.do(e.client(), http.MethodGet, "/stats", nil, nil)
	if n := strings.Count(body, `href="/feedback?from=%2Fstats"`); n != 1 {
		t.Errorf("a demo page has %d feedback links, want 1 (nav)", n)
	}
	code, body := e.do(e.client(), http.MethodGet, "/feedback?from=%2Fstats", nil, nil)
	if code.StatusCode != http.StatusOK || !strings.Contains(body, "https://mail.google.com/mail/?view=cm") ||
		!strings.Contains(body, "https://outlook.live.com/mail/0/deeplink/compose") || !strings.Contains(body, `href="mailto:hello@example.com?`) ||
		!strings.Contains(body, `<code>/stats</code>`) || !strings.Contains(body, `data-copy="#fb-addr"`) {
		t.Errorf("feedback page: %d", code.StatusCode)
	}
	plain := newTestEnv(t)
	if _, body := plain.do(plain.client(), http.MethodGet, "/stats", nil, nil); strings.Contains(body, "/feedback") {
		t.Error("a feedback link without HUNTDECK_FEEDBACK_EMAIL")
	}
	if code, _ := plain.do(plain.client(), http.MethodGet, "/feedback", nil, nil); code.StatusCode != http.StatusNotFound {
		t.Errorf("feedback page without an address: %d, want 404", code.StatusCode)
	}
}
