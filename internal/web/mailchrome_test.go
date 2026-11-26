package web

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/overrides"
)

// TestMailDetailChromeIsTranslated reads the message page's interface copy in
// Chinese. The content assertions next door prove escaping and attachments; this
// one is about the page around them, which no screenshot could reach: the instance
// has no HuntMail, so only the fixture can render a message.
func TestMailDetailChromeIsTranslated(t *testing.T) {
	mail := newFakeMail()
	src := config.Source{LookupEnv: func(string) (string, bool) { return "", false }, ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist }}
	e := newTestEnv(t, func(d *Deps) { d.Mail, d.Accounts = mail, overrides.New(d.Store, t.TempDir(), src) })
	c := e.client()

	// The fake serves its own messages under ids m1, m2.
	res, body := e.do(c, "GET", "/email/m1", nil, http.Header{"Accept-Language": {"zh-CN"}})
	if res.StatusCode != 200 {
		t.Fatalf("/email/m1 = %d", res.StatusCode)
	}
	for _, raw := range []string{">From<", ">To<", ">Subject<", ">Reply<", ">Delete<"} {
		if strings.Contains(body, raw) {
			t.Errorf("the message page prints %q in English", raw)
		}
	}
}
