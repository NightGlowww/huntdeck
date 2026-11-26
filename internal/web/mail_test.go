package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/huntmail"
	"github.com/mdpsec/huntdeck/internal/overrides"
)

// fakeMail is a HuntMail inbox in memory; it remembers the last list's
// filter.
type fakeMail struct {
	mu     sync.Mutex
	emails []huntmail.Email
	last   huntmail.Filter
	err    error
	old    bool              // a HuntMail before 1.1: no daily counts, no to_domain
	nl     bool              // a HuntMail that tells newsletters apart
	blocks *[]huntmail.Block // a HuntMail with blocks: its blocks
	// senders are a HuntMail's top senders, nil for one without them;
	// sendersAsked the window it was asked for.
	senders      []huntmail.Sender
	sendersAsked [2]int
}

func (f *fakeMail) Senders(_ context.Context, days, limit int) ([]huntmail.Sender, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.senders == nil {
		return nil, huntmail.ErrNotFound
	}
	f.sendersAsked = [2]int{days, limit}
	return slices.Clone(f.senders), nil
}

func (f *fakeMail) Blocks(context.Context) ([]huntmail.Block, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.blocks == nil {
		return nil, huntmail.ErrNotFound
	}
	return slices.Clone(*f.blocks), nil
}

// AddBlock refuses a sender that isn't a domain, as HuntMail does, and
// sets the scope of one it has.
func (f *fakeMail) AddBlock(_ context.Context, b huntmail.NewBlock) (huntmail.Block, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b.Kind == huntmail.BlockSender && !strings.Contains(b.Value, ".") {
		return huntmail.Block{}, &huntmail.APIError{Op: "add a block", Status: 400, Text: "a sender block takes a domain name"}
	}
	for i, x := range *f.blocks {
		if x.Kind == b.Kind && x.Value == b.Value {
			(*f.blocks)[i].Scope = b.Scope
			return (*f.blocks)[i], nil
		}
	}
	nb := huntmail.Block{ID: int64(len(*f.blocks) + 1), Kind: b.Kind, Value: b.Value, Scope: b.Scope, CreatedAt: time.Now()}
	*f.blocks = append([]huntmail.Block{nb}, *f.blocks...)
	return nb, nil
}

func (f *fakeMail) DeleteBlock(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, x := range *f.blocks {
		if x.ID == id {
			*f.blocks = slices.Delete(*f.blocks, i, i+1)
			return nil
		}
	}
	return huntmail.ErrNotFound
}

func (f *fakeMail) List(_ context.Context, flt huntmail.Filter) (huntmail.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = flt
	if f.err != nil {
		return huntmail.Page{}, f.err
	}
	var out []huntmail.Email
	for _, e := range f.emails {
		if (flt.To == "" || e.To == flt.To) && (flt.ToPrefix == "" || strings.HasPrefix(e.To, flt.ToPrefix)) &&
			(flt.ToDomain == "" || strings.HasSuffix(e.To, "@"+flt.ToDomain)) &&
			(!f.nl || flt.Newsletters == "" || e.Newsletter == (flt.Newsletters == huntmail.NewslettersOnly)) {
			out = append(out, e)
		}
	}
	// The cursor is where the page starts, as text.
	start, _ := strconv.Atoi(flt.Cursor)
	out = out[min(start, len(out)):]
	page := huntmail.Page{Emails: out}
	if flt.Limit > 0 && len(out) > flt.Limit {
		page.Emails, page.NextCursor = out[:flt.Limit], strconv.Itoa(start+flt.Limit)
	}
	return page, nil
}

func (f *fakeMail) Get(_ context.Context, id string) (huntmail.Email, error) {
	for _, e := range f.emails {
		if e.ID == id {
			return e, nil
		}
	}
	return huntmail.Email{}, huntmail.ErrNotFound
}

func (f *fakeMail) View(_ context.Context, id string, images bool) (huntmail.ViewLink, error) {
	u := "https://huntmail.example.workers.dev/base/view/" + id + "?t=sig"
	if images {
		u += "&images=1"
	}
	return huntmail.ViewLink{URL: u, ExpiresAt: time.Now().Add(15 * time.Minute)}, nil
}

func (f *fakeMail) Attachment(context.Context, string, int) (huntmail.File, error) {
	return huntmail.File{Name: `evil".html`, ContentType: "text/html", Body: []byte("<script>alert(1)</script>")}, nil
}

func (f *fakeMail) Stats(_ context.Context, days int) (huntmail.Stats, error) {
	if f.err != nil {
		return huntmail.Stats{}, f.err
	}
	st := huntmail.Stats{Version: "1.0.0", Messages: int64(len(f.emails)), StoredBytes: 2 << 20, MaxStoreBytes: 350e6, RetentionDays: 30,
		Domains:      []huntmail.Domain{{Domain: "example.com", Received: 40, Today: 3}, {Domain: "example.net", Received: 10}},
		DomainsSetUp: []string{"example.com", "example.net", "example.org"}, ParseDeferred: 7}
	if f.blocks != nil {
		n := int64(len(*f.blocks) * 2)
		st.Blocked = &n
	}
	if f.nl {
		n := int64(0)
		for _, e := range f.emails {
			n += int64(boolInt(e.Newsletter))
		}
		st.Newsletters = &n
	}
	for i := range days * (1 - boolInt(f.old)) {
		st.Days = append(st.Days, huntmail.Day{Day: time.Date(2026, 11, 18-days+1+i, 0, 0, 0, 0, time.UTC).Format(time.DateOnly), Received: int64(i)})
	}
	return st, nil
}

func newFakeMail() *fakeMail {
	at := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	return &fakeMail{emails: []huntmail.Email{
		{ID: "m1", ReceivedAt: at, To: "acmecorp-1763460000-k7q2x@example.com", From: "bounce@mailer.target.example",
			HeaderFrom: "Target <no-reply@target.example>", Subject: `Your code <script>alert(1)</script>`, HasHTML: true,
			Text: "Your code is 482913 <b>bold</b>", Codes: []string{"482913"},
			LinkDetails: []huntmail.Link{{URL: "https://target.example/verify?t=abc", Text: "Verify <i>email</i>"}, {URL: "javascript:alert(1)", Text: "Click"}},
			Attachments: []huntmail.Attachment{{Filename: "terms.pdf", MimeType: "application/pdf", Size: 2048}},
			Auth:        huntmail.Auth{SPF: "pass", DKIM: "pass", DMARC: "pass", VerifiedDomains: []string{"target.example"}}, ParseStatus: "ok"},
		{ID: "m2", ReceivedAt: at.Add(-time.Hour), To: "news-1@example.net", From: "news@other.example", Subject: "Welcome",
			Auth: huntmail.Auth{SPF: "none", DKIM: "none", DMARC: "none"}, ParseStatus: "deferred"},
	}}
}

// Without HuntMail: the Email page says how to set it up, and programs
// have no Email tab.
func TestEmailNotSetUp(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	if res, body := e.do(c, "GET", "/email", nil, nil); res.StatusCode != http.StatusOK || !strings.Contains(body, "Set up HuntMail") {
		t.Errorf("/email: %d", res.StatusCode)
	}
	if _, body := e.do(c, "GET", "/programs/fake/acme", nil, nil); strings.Contains(body, "?tab=email") {
		t.Error("an Email tab without HuntMail")
	}
	if res, _ := e.do(c, "POST", "/programs/fake/acme/email/new", url.Values{"domain": {"example.com"}}, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("new address without HuntMail: %d", res.StatusCode)
	}
}

func TestEmail(t *testing.T) {
	mail := newFakeMail()
	src := config.Source{LookupEnv: func(string) (string, bool) { return "", false }, ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist }}
	e := newTestEnv(t, func(d *Deps) { d.Mail, d.Accounts = mail, overrides.New(d.Store, t.TempDir(), src) })
	c := e.client()

	// A program's tab: no addresses yet, then one made on the domain asked for.
	_, body := e.do(c, "GET", "/programs/fake/acme?tab=email", nil, nil)
	if !strings.Contains(body, `action="/programs/fake/acme/email/new"`) || !strings.Contains(body, `<option value="example.net">`) {
		t.Fatalf("the Email tab has no new-address form:\n%s", body)
	}
	res, _ := e.do(c, "POST", "/programs/fake/acme/email/new", url.Values{"domain": {"example.net"}, "note": {"admin"}}, nil)
	if res.StatusCode != http.StatusSeeOther || !strings.Contains(res.Header.Get("Location"), "?tab=email&made=") {
		t.Fatalf("new address: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if res, _ := e.do(c, "POST", "/programs/fake/acme/email/new", url.Values{"domain": {"attacker.example"}}, nil); res.StatusCode != http.StatusBadRequest {
		t.Errorf("an address on a domain HuntMail doesn't have: %d", res.StatusCode)
	}
	p, _ := e.store.Program(context.Background(), "fake", "acme")
	addrs, _ := e.store.MailAddresses(context.Background(), p.ID)
	if len(addrs) != 1 || !strings.HasPrefix(addrs[0].Address, "acmecorp-") || !strings.HasSuffix(addrs[0].Address, "@example.net") || addrs[0].Note != "admin" {
		t.Fatalf("addresses: %+v", addrs)
	}
	_, body = e.do(c, "GET", res.Header.Get("Location"), nil, nil)
	if !strings.Contains(body, addrs[0].Address) || mail.last.ToPrefix != "acmecorp-" || !strings.Contains(body, `href="/email/m1"`) {
		t.Errorf("the tab lists the address and the program's mail (filter %+v)", mail.last)
	}

	// The inbox: the program first, the code to copy, the sender's words escaped.
	_, body = e.do(c, "GET", "/email", nil, nil)
	for _, want := range []string{`<th class="c-prog">Program</th>`, `href="/programs/fake/acme?tab=email"`, `data-copy-text="482913"`,
		"Your code &lt;script&gt;", `href="/email/m2"`} {
		if !strings.Contains(body, want) {
			t.Errorf("/email lacks %q", want)
		}
	}
	if strings.Contains(body, "<script>alert") {
		t.Error("/email shows the sender's HTML")
	}
	_, _ = e.do(c, "GET", "/email?program="+itoa(p.ID)+"&q=code", nil, nil)
	if mail.last.ToPrefix != "acmecorp-" || mail.last.Q != "code" || !mail.last.NoText {
		t.Errorf("program filter: %+v", mail.last)
	}

	// A message, as text.
	_, body = e.do(c, "GET", "/email/m1", nil, nil)
	for _, want := range []string{"Your code is 482913 &lt;b&gt;bold&lt;/b&gt;", "Verify &lt;i&gt;email&lt;/i&gt;", `href="/email/m1/view"`,
		`href="/email/m1/attachments/0"`, `">verified</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("/email/m1 lacks %q", want)
		}
	}
	if strings.Contains(body, `href="javascript:`) {
		t.Error("a javascript: link can be opened")
	}
	if res, _ := e.do(c, "GET", "/email/m1/view?images=1", nil, nil); res.StatusCode != http.StatusSeeOther ||
		!strings.HasPrefix(res.Header.Get("Location"), "https://huntmail.example.workers.dev/") || res.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("full view: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	res, body = e.do(c, "GET", "/email/m1/attachments/0", nil, nil)
	if res.Header.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment;") ||
		res.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(body, "<script>") {
		t.Errorf("attachment headers: %v", res.Header)
	}
	if res, _ := e.do(c, "GET", "/email/nope", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("no such message: %d", res.StatusCode)
	}

	// Forgetting an address.
	if res, _ := e.do(c, "POST", "/programs/fake/acme/email/"+itoa(addrs[0].ID)+"/forget", url.Values{}, nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("forget: %d", res.StatusCode)
	}
	if as, _ := e.store.MailAddresses(context.Background(), p.ID); len(as) != 0 {
		t.Errorf("forgotten: %+v", as)
	}

	// HuntMail's Setup tab shows it works; a refused key says what to do.
	if _, body := e.do(c, "GET", "/email/setup", nil, nil); !strings.Contains(body, `id="acct-huntmail"`) || !strings.Contains(body, "connected") {
		t.Error("HuntMail › Setup lacks HuntMail's box or its status")
	}
	mail.err = huntmail.ErrUnauthorized
	if _, body := e.do(c, "GET", "/email", nil, nil); !strings.Contains(body, "HuntMail refused the API key") {
		t.Error("a refused key isn't explained")
	}
}

// A read-only copy never serves mail: Email and HuntMail are a tour (a
// picture with made-up mail), HuntMail is never asked, the rest is a 404.
func TestEmailReadOnly(t *testing.T) {
	mail := newFakeMail()
	mail.err = errors.New("asked HuntMail")
	e := newTestEnv(t, func(d *Deps) { d.ReadOnly, d.Mail = true, mail })
	for p, shot := range map[string]string{"/email": "tour/email.webp", "/email/stats": "tour/huntmail.webp"} {
		res, body := e.do(e.client(), "GET", p, nil, nil)
		if res.StatusCode != http.StatusOK || !strings.Contains(body, shot) || strings.Contains(body, "asked HuntMail") || strings.Contains(body, "data-mail=") {
			t.Errorf("%s in a read-only copy: %d, not the tour", p, res.StatusCode)
		}
	}
	if mail.last != (huntmail.Filter{}) {
		t.Errorf("a read-only copy asked HuntMail for %+v", mail.last)
	}
	for _, p := range []string{"/email/m1", "/email/m1/view", "/email/m1/pane", "/email/blocks"} {
		if res, _ := e.do(e.client(), "GET", p, nil, nil); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s in a read-only copy: %d", p, res.StatusCode)
		}
	}
	if res, _ := e.do(e.client(), "POST", "/email/blocks", url.Values{"kind": {"sender"}, "value": {"x.example"}}, nil); res.StatusCode == http.StatusSeeOther {
		t.Error("a read-only copy added a block")
	}
	if _, body := e.do(e.client(), "GET", "/", nil, nil); !strings.Contains(body, `href="/email"`) {
		t.Error("a read-only copy's sidebar has no Email tour")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// The inbox guesses a program from the sender's domain, pages through the
// mail and keeps to a domain; Email › HuntMail shows HuntMail's numbers,
// and its settings on its Setup tab, not in Settings.
func TestEmailListAndStats(t *testing.T) {
	mail := newFakeMail()
	at := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	mail.emails = append(mail.emails,
		huntmail.Email{ID: "s1", ReceivedAt: at, To: "signup1@example.com", From: "bounce@mailer.example.org", HeaderFrom: "Acme <hello@app.acme.example>"},
		huntmail.Email{ID: "s2", ReceivedAt: at, To: "signup2@example.com", From: "x@hackerone.com", HeaderFrom: "HackerOne <no-reply@hackerone.com>"})
	for i := range 60 {
		mail.emails = append(mail.emails, huntmail.Email{ID: "b" + strconv.Itoa(i), ReceivedAt: at.Add(-time.Duration(i) * time.Minute), To: "bulk@example.net", From: "n@news.example"})
	}
	src := config.Source{LookupEnv: func(string) (string, bool) { return "", false }, ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist }}
	e := newTestEnv(t, func(d *Deps) { d.Mail, d.Accounts = mail, overrides.New(d.Store, t.TempDir(), src) })
	c := e.client()

	_, body := e.do(c, "GET", "/email", nil, nil)
	row := func(body, id string) string {
		i := strings.Index(body, `data-href="/email/`+id+`"`)
		if i < 0 {
			return ""
		}
		return body[i:min(len(body), i+1500)]
	}
	if r := row(body, "s1"); !strings.Contains(r, "mguess") || !strings.Contains(r, `href="/programs/fake/acme"`) || !strings.Contains(r, "acme.example") {
		t.Errorf("no guess from the sender's domain:\n%s", r)
	}
	if r := row(body, "s2"); strings.Contains(r, "mguess") {
		t.Error("a platform's own mail was guessed to be a program's")
	}
	if !strings.Contains(body, "1–30 of 64") || !strings.Contains(body, "page 1") || !strings.Contains(body, `rel="next"`) {
		t.Fatalf("the first page's pager:\n%s", body[strings.Index(body, "mpager"):])
	}
	next := hrefOf(body, `rel="next"`)
	_, body = e.do(c, "GET", next, nil, nil)
	if !strings.Contains(body, "31–60 of 64") || !strings.Contains(body, "page 2") || hrefOf(body, `rel="prev"`) != "/email" {
		t.Errorf("page 2 (%s), back to %q", next, hrefOf(body, `rel="prev"`))
	}
	_, body = e.do(c, "GET", hrefOf(body, `rel="next"`), nil, nil)
	if !strings.Contains(body, "61–64 of 64") || !strings.Contains(body, "page 3") || hrefOf(body, `rel="prev"`) != next {
		t.Errorf("page 3, back to %q", hrefOf(body, `rel="prev"`))
	}

	_, body = e.do(c, "GET", "/email?domain=example.net", nil, nil)
	if mail.last.ToDomain != "example.net" || strings.Contains(body, `href="/email/s1"`) || !strings.Contains(body, `<option value="example.net" selected>`) {
		t.Errorf("domain filter: %+v", mail.last)
	}

	_, body = e.do(c, "GET", "/email/stats", nil, nil)
	for _, want := range []string{"<h1 class=\"m-heading\">HuntMail</h1>", `class="bchart"`, `href="/email?domain=example.com"`, "example.org", ">1.0.0<", "of 350.0 MB"} {
		if !strings.Contains(body, want) {
			t.Errorf("/email/stats lacks %q", want)
		}
	}

	if _, body := e.do(c, "GET", "/settings?tab=accounts", nil, nil); strings.Contains(body, `id="acct-huntmail"`) {
		t.Error("HuntMail's box is still on Accounts")
	}
	if _, body := e.do(c, "GET", "/settings", nil, nil); strings.Contains(body, `href="/settings?tab=email"`) {
		t.Error("Settings still has an Email tab")
	}
	for old, want := range map[string]string{"/settings?tab=email": "/email/setup", "/settings/huntmail": "/email/setup"} {
		if res, _ := e.do(c, "GET", old, nil, nil); res.StatusCode != http.StatusMovedPermanently || res.Header.Get("Location") != want {
			t.Errorf("%s: %d to %q", old, res.StatusCode, res.Header.Get("Location"))
		}
	}
	if _, body := e.do(c, "GET", "/email/setup", nil, nil); !strings.Contains(body, `id="acct-huntmail"`) || !strings.Contains(body, `href="/email/stats"`) {
		t.Error("HuntMail › Setup lacks HuntMail's box or the Overview tab")
	}
	res, _ := e.do(c, "POST", "/settings/accounts", url.Values{"group": {"huntmail"}, "HUNTMAIL_DOMAINS": {"example.com"}}, nil)
	if loc := res.Header.Get("Location"); !strings.HasPrefix(loc, "/email/setup?msg=") {
		t.Errorf("saving HuntMail goes back to %q", loc)
	}
	res, body = e.do(c, "POST", "/settings/accounts", url.Values{"group": {"huntmail"}, "HUNTMAIL_URL": {"not a url"}}, nil)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Not saved") || !strings.Contains(body, `id="acct-huntmail"`) {
		t.Errorf("a bad address: %d", res.StatusCode)
	}
}

// hrefOf is the address of the first link with attr.
func hrefOf(body, attr string) string {
	i := strings.Index(body, attr)
	if i < 0 {
		return ""
	}
	j := strings.LastIndex(body[:i], `href="`)
	if j < 0 {
		return ""
	}
	return strings.ReplaceAll(body[j+6:j+6+strings.Index(body[j+6:], `"`)], "&amp;", "&")
}

func TestVersionLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"1.0.0", "1.1.0", true}, {"1.1.0", "1.0.0", false}, {"1.0.0", "1.0.0", false}, {"1.9", "1.10", true}, {"1.0", "1.0.1", true}, {"2.0.0", "1.99.0", false}} {
		if got := versionLess(c.a, c.b); got != c.want {
			t.Errorf("versionLess(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// An older HuntMail can't keep to a domain: the filter isn't offered.
func TestEmailOldHuntMail(t *testing.T) {
	mail := newFakeMail()
	mail.old = true
	e := newTestEnv(t, func(d *Deps) { d.Mail = mail })
	if _, body := e.do(e.client(), "GET", "/email", nil, nil); strings.Contains(body, `name="domain"`) {
		t.Error("the domain filter is offered")
	}
	if _, body := e.do(e.client(), "GET", "/email?domain=example.com", nil, nil); !strings.Contains(body, "can&#39;t keep to a domain") || mail.last.ToDomain != "" {
		t.Errorf("a domain asked of an old HuntMail: %+v", mail.last)
	}
	if _, body := e.do(e.client(), "GET", "/email/stats", nil, nil); !strings.Contains(body, "doesn&#39;t send its daily counts") {
		t.Error("the stats don't say why there's no chart")
	}
}

func TestMailAction(t *testing.T) {
	row := func(links ...huntmail.Link) mailRow { return mailRow{Email: huntmail.Email{LinkDetails: links}} }
	for name, c := range map[string]struct {
		row        mailRow
		kind, link string
	}{
		"by its text": {row(huntmail.Link{URL: "https://t.example/u?x=1", Text: "Unsubscribe"}, huntmail.Link{URL: "https://t.example/a?t=1", Text: "Verify your email"}),
			"Verify", "https://t.example/a?t=1"},
		"by its address":         {row(huntmail.Link{URL: "https://t.example/sign-in/verify?token=abc"}), "Verify", "https://t.example/sign-in/verify?token=abc"},
		"a reset by its address": {row(huntmail.Link{URL: "https://auth.t.example/password/change/tok123", Text: "Click here"}), "Reset", "https://auth.t.example/password/change/tok123"},
		"a reset":                {row(huntmail.Link{URL: "https://t.example/r?t=1", Text: "Choose a new password"}), "Reset", "https://t.example/r?t=1"},
		"a magic link":           {row(huntmail.Link{URL: "https://t.example/m?t=1", Text: "Use this magic link"}), "Sign in", "https://t.example/m?t=1"},
		"an invitation":          {row(huntmail.Link{URL: "https://t.example/i?t=1", Text: "Accept the invitation"}), "Invite", "https://t.example/i?t=1"},
		"verify beats an earlier invite": {row(huntmail.Link{URL: "https://t.example/i", Text: "Accept invitation"}, huntmail.Link{URL: "https://t.example/v", Text: "Confirm your account"}),
			"Verify", "https://t.example/v"},
		"never javascript:":            {row(huntmail.Link{URL: "javascript:alert(1)", Text: "Verify"}), "", ""},
		"a newsletter":                 {row(huntmail.Link{URL: "https://news.example/a", Text: "Read more"}, huntmail.Link{URL: "https://news.example/unsubscribe?confirm=1", Text: "Unsubscribe"}), "", ""},
		"social links":                 {row(huntmail.Link{URL: "https://www.facebook.com/confirm", Text: "Facebook"}), "", ""},
		"a tracking token":             {row(huntmail.Link{URL: "https://link.news.example/v2/emailclick?d=btkVEVerifLygm3"}), "", ""},
		"a tracking token as its text": {row(huntmail.Link{URL: "https://link.news.example/v2/emailclick?d=btkVEVerifLygm3", Text: "https://link.news.example/v2/emailclick?d=btkVEVerifLygm3"}), "", ""},
		"a word inside a path":         {row(huntmail.Link{URL: "https://news.example/a/xverifyz"}), "", ""},
		"an invite by its address": {row(huntmail.Link{URL: "https://cloud.example/account/accept-invite?source=x", Text: "https://cloud.example/account/accept-invite?source=x"}),
			"Invite", "https://cloud.example/account/accept-invite?source=x"},
		"HuntMail's own pick": {mailRow{Email: huntmail.Email{Action: &huntmail.Action{Kind: "Reset", URL: "https://t.example/r", Text: "Reset"},
			LinkDetails: []huntmail.Link{{URL: "https://t.example/v", Text: "Verify"}}}}, "Reset", "https://t.example/r"},
		"HuntMail's pick, not http": {mailRow{Email: huntmail.Email{Action: &huntmail.Action{Kind: "Verify", URL: "javascript:x"}}}, "", ""},
	} {
		a := c.row.Action()
		switch {
		case c.kind == "" && a != nil:
			t.Errorf("%s: %+v", name, a)
		case c.kind != "" && (a == nil || a.Kind != c.kind || a.URL != c.link):
			t.Errorf("%s: %+v, want %s %s", name, a, c.kind, c.link)
		}
	}
}

// A message opens in the Email page's pane: as a fragment for the script,
// and with ?open= on the page itself, its row marked.
func TestEmailPane(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) { d.Mail = newFakeMail() })
	c := e.client()
	res, body := e.do(c, "GET", "/email/m1/pane", nil, nil)
	if res.StatusCode != http.StatusOK || strings.Contains(body, "<html") || !strings.Contains(body, `class="mpane-title"`) ||
		!strings.Contains(body, "Verify link") || strings.Contains(body, "<script>alert") {
		t.Errorf("the pane fragment: %d\n%s", res.StatusCode, body)
	}
	if _, body := e.do(c, "GET", "/email/nope/pane", nil, nil); !strings.Contains(body, "No such message") {
		t.Error("a missing message's pane")
	}
	_, body = e.do(c, "GET", "/email?open=m1", nil, nil)
	if !strings.Contains(body, `data-mail="m1" class="on"`) || !strings.Contains(body, `class="mpane-title"`) || !strings.Contains(body, "mailpane.js") {
		t.Error("?open= doesn't show the message beside the list")
	}
	if _, body := e.do(c, "GET", "/email?open=../x", nil, nil); strings.Contains(body, `class="mpane-title"`) {
		t.Error("a bad id was opened")
	}
	// m1 has a code and a verify link: the list shows the code.
	if _, body := e.do(c, "GET", "/email", nil, nil); strings.Contains(body, `>Verify <svg`) || !strings.Contains(body, `data-copy-text="482913"`) {
		t.Error("a code doesn't come before a link in the list")
	}
}

// A HuntMail that tells newsletters apart: the Email page and a program's
// tab leave them out unless asked (Mail, Newsletters, All); one that
// can't offers no toggle and lists everything.
func TestEmailNewsletters(t *testing.T) {
	mail := newFakeMail()
	mail.nl = true
	at := time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC)
	mail.emails = append(mail.emails,
		huntmail.Email{ID: "n1", ReceivedAt: at, To: "acmecorp-1763460000-k7q2x@example.com", From: "news@target.example", Subject: "This week", Newsletter: true, ParseStatus: "ok"},
		huntmail.Email{ID: "n2", ReceivedAt: at, To: "other@example.com", From: "news@shop.example", Subject: "Sale", Newsletter: true, ParseStatus: "ok"})
	src := config.Source{LookupEnv: func(string) (string, bool) { return "", false }, ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist }}
	e := newTestEnv(t, func(d *Deps) { d.Mail, d.Accounts = mail, overrides.New(d.Store, t.TempDir(), src) })
	c := e.client()

	for _, tc := range []struct {
		path, filter string
		has, hasNot  []string
	}{
		{"/email", huntmail.NewslettersHide, []string{`href="/email/m1"`, `class="on" aria-current="true" href="/email"`, "Mail <em>2</em>", "Newsletters <em>2</em>", "All <em>4</em>", "· 2 messages"},
			[]string{`href="/email/n1"`, `name="nl"`}},
		{"/email?nl=show", "", []string{`href="/email/m1"`, `href="/email/n1"`, `class="chip mnl"`, `name="nl" value="show"`, "· 4 messages"}, nil},
		{"/email?nl=only", huntmail.NewslettersOnly, []string{`href="/email/n2"`, "· 2 newsletters", `href="/email?nl=only" title`}, []string{`href="/email/m1"`, `class="chip mnl"`}},
		{"/email?nl=bogus", huntmail.NewslettersHide, []string{`class="on" aria-current="true" href="/email"`}, []string{`href="/email/n1"`}},
	} {
		_, body := e.do(c, "GET", tc.path, nil, nil)
		if mail.last.Newsletters != tc.filter {
			t.Errorf("%s asked HuntMail for newsletters=%q", tc.path, mail.last.Newsletters)
		}
		for _, want := range tc.has {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q", tc.path, want)
			}
		}
		for _, bad := range tc.hasNot {
			if strings.Contains(body, bad) {
				t.Errorf("%s has %q", tc.path, bad)
			}
		}
	}
	if _, body := e.do(c, "GET", "/email?nl=only&q=x", nil, nil); !strings.Contains(body, `name="nl" value="only"`) || strings.Contains(body, "<em>") {
		t.Error("a filtered list keeps its view and shows no counts")
	}

	// A program's tab, by its addresses.
	if res, _ := e.do(c, "POST", "/programs/fake/acme/email/new", url.Values{"domain": {"example.com"}}, nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("new address: %d", res.StatusCode)
	}
	_, body := e.do(c, "GET", "/programs/fake/acme?tab=email", nil, nil)
	if mail.last.Newsletters != huntmail.NewslettersHide || strings.Contains(body, `href="/email/n1"`) || !strings.Contains(body, `href="/programs/fake/acme?tab=email&amp;nl=show#mail-list"`) {
		t.Errorf("the program's tab shows newsletters (filter %+v)", mail.last)
	}
	if _, body := e.do(c, "GET", "/programs/fake/acme?tab=email&nl=show", nil, nil); mail.last.Newsletters != "" || !strings.Contains(body, `href="/email/n1"`) {
		t.Errorf("the program's tab with newsletters (filter %+v)", mail.last)
	}

	if _, body := e.do(c, "GET", "/email/stats", nil, nil); !strings.Contains(body, ">2 newsletters</a>") {
		t.Error("Mail stats doesn't count newsletters")
	}

	// A HuntMail that can't tell: no toggle, everything listed.
	old := newFakeMail()
	old.emails = mail.emails
	e = newTestEnv(t, func(d *Deps) { d.Mail = old })
	_, body = e.do(e.client(), "GET", "/email?nl=only", nil, nil)
	if old.last.Newsletters != "" || strings.Contains(body, `aria-label="Newsletters"`) || !strings.Contains(body, `href="/email/m1"`) {
		t.Errorf("a HuntMail without newsletters: filter %+v", old.last)
	}
}

// Blocks: HuntMail's, listed, added from the page or a message, refused
// with HuntMail's reason, switched and removed; none with a HuntMail
// that has no blocks.
func TestEmailBlocks(t *testing.T) {
	mail := newFakeMail()
	mail.emails[0].SenderDomain = "news.target.example"
	src := config.Source{LookupEnv: func(string) (string, bool) { return "", false }, ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist }}
	e := newTestEnv(t, func(d *Deps) { d.Mail, d.Accounts = mail, overrides.New(d.Store, t.TempDir(), src) })
	c := e.client()

	// Without blocks: the page says why, a message offers none.
	if _, body := e.do(c, "GET", "/email/stats", nil, nil); !strings.Contains(body, "came with HuntMail 1.3") {
		t.Error("the HuntMail page without blocks doesn't say why")
	}
	if _, body := e.do(c, "GET", "/email/m1", nil, nil); strings.Contains(body, `action="/email/blocks"`) {
		t.Error("a message offers blocks HuntMail doesn't have")
	}

	mail.blocks = &[]huntmail.Block{}
	e = newTestEnv(t, func(d *Deps) { d.Mail, d.Accounts = mail, overrides.New(d.Store, t.TempDir(), src) })
	c = e.client()
	if res, _ := e.do(c, "POST", "/programs/fake/acme/email/new", url.Values{"domain": {"example.com"}}, nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("new address: %d", res.StatusCode)
	}
	_, body := e.do(c, "GET", "/email/m1/pane", nil, nil)
	for _, want := range []string{`name="value" value="news.target.example"`, `name="value" value="target.example"`,
		`name="value" value="acmecorp-1763460000-k7q2x@example.com"`, `name="value" value="acmecorp-"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the message's Block box lacks %q", want)
		}
	}

	res, _ := e.do(c, "POST", "/email/blocks", url.Values{"kind": {"sender"}, "value": {" target.example "}, "scope": {"all"}}, nil)
	if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), "/email/stats?added=1") {
		t.Fatalf("add: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	_, body = e.do(c, "GET", res.Header.Get("Location"), nil, nil)
	if !strings.Contains(body, `id="block-1" class="on"`) || !strings.Contains(body, `All mail from <b class="mono">target.example</b>`) {
		t.Errorf("the new block isn't listed and pointed out")
	}
	if res, _ := e.do(c, "POST", "/email/blocks", url.Values{"kind": {"sender"}, "value": {"target.example"}, "scope": {"newsletters"}}, nil); res.StatusCode != http.StatusSeeOther ||
		(*mail.blocks)[0].Scope != huntmail.BlockNewsletters {
		t.Errorf("switching to newsletters: %d %+v", res.StatusCode, *mail.blocks)
	}
	if res, _ := e.do(c, "POST", "/email/blocks", url.Values{"kind": {"prefix"}, "value": {"acmecorp-"}, "scope": {"newsletters"}}, nil); res.StatusCode != http.StatusSeeOther ||
		(*mail.blocks)[0].Scope != huntmail.BlockAll {
		t.Errorf("a prefix block is for all mail: %d %+v", res.StatusCode, *mail.blocks)
	}
	if _, body := e.do(c, "GET", "/email/stats", nil, nil); !strings.Contains(body, `<b class="mono">acmecorp-</b> <span class="muted small">(`) {
		t.Error("a program's prefix block doesn't name the program")
	}

	res, body = e.do(c, "POST", "/email/blocks", url.Values{"kind": {"sender"}, "value": {"localhost"}}, nil)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "HuntMail didn&#39;t add it: a sender block takes a domain name.") || !strings.Contains(body, `value="localhost"`) {
		t.Errorf("a refused block: %d", res.StatusCode)
	}
	if res, _ := e.do(c, "POST", "/email/blocks", url.Values{"kind": {"subject"}, "value": {"x"}}, nil); res.StatusCode != http.StatusBadRequest {
		t.Errorf("an unknown kind: %d", res.StatusCode)
	}

	if res, _ := e.do(c, "POST", "/email/blocks/1/delete", url.Values{}, nil); res.StatusCode != http.StatusSeeOther || len(*mail.blocks) != 1 {
		t.Errorf("remove: %d %+v", res.StatusCode, *mail.blocks)
	}
	if res, _ := e.do(c, "POST", "/email/blocks/99/delete", url.Values{}, nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("removing one that's gone: %d", res.StatusCode)
	}
	_, body = e.do(c, "GET", "/email/stats", nil, nil)
	if !strings.Contains(body, ">Blocked<") || !strings.Contains(body, `id="mail-blocks"`) || !strings.Contains(body, ">Unblock</button>") {
		t.Error("Mail stats doesn't list the blocks to unblock")
	}
	id := strconv.FormatInt((*mail.blocks)[0].ID, 10)
	if res, _ := e.do(c, "POST", "/email/blocks/"+id+"/delete", url.Values{}, nil); res.Header.Get("Location") != "/email/stats#mail-blocks" || len(*mail.blocks) != 0 {
		t.Errorf("unblocking goes to %q", res.Header.Get("Location"))
	}
	if res, _ := e.do(c, "GET", "/email/blocks", nil, nil); res.Header.Get("Location") != "/email/stats#mail-blocks" {
		t.Errorf("the old Blocks page goes to %q", res.Header.Get("Location"))
	}
}

// The program a message is for, when HuntDeck didn't make its address: by
// the address's name, the sender's domain in scope, or the sender's domain
// named as the program is; never by a common word.
func TestEmailGuesses(t *testing.T) {
	at := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	mail := &fakeMail{emails: []huntmail.Email{
		{ID: "g1", ReceivedAt: at, To: "acme-fixverify-1795485606@example.com", From: "x@mailer.unrelated.example"},
		{ID: "g2", ReceivedAt: at, To: "plain@example.com", From: "b@bounce.example", HeaderFrom: "Acme <no-reply@acmecorp.example>"},
		{ID: "g3", ReceivedAt: at, To: "plain2@example.com", From: "no-reply@app.acme.example"},
		{ID: "g4", ReceivedAt: at, To: "news-1@example.com", From: "no-reply@shopper.example"},
		{ID: "g5", ReceivedAt: at, To: "acme.corp.7@example.com", From: "n@hackerone.com"},
	}}
	e := newTestEnv(t, func(d *Deps) { d.Mail = mail })
	_, body := e.do(e.client(), "GET", "/email", nil, nil)
	row := func(id string) string {
		i := strings.Index(body, `data-href="/email/`+id+`"`)
		if i < 0 {
			t.Fatalf("no row %s", id)
		}
		r := body[i:]
		return r[:strings.Index(r, "</tr>")]
	}
	for id, why := range map[string]string{
		"g1": "the address starts with acme, as this program is named",
		"g2": "the sender&#39;s domain, acmecorp.example, is named as this program is",
		"g3": "the sender&#39;s domain, acme.example, is in this program&#39;s scope",
		"g5": "the address starts with acmecorp, as this program is named",
	} {
		if r := row(id); !strings.Contains(r, why) || !strings.Contains(r, `href="/programs/fake/acme"`) {
			t.Errorf("%s isn't guessed as acme because %s:\n%s", id, why, r)
		}
	}
	if strings.Contains(row("g4"), "mguess") {
		t.Error("an address starting with a common word was guessed")
	}
	if _, body := e.do(e.client(), "GET", "/email/g2", nil, nil); !strings.Contains(body, "a guess: the sender") {
		t.Error("the message doesn't show the guess")
	}
	if _, body := e.do(e.client(), "GET", "/email/g2/pane", nil, nil); strings.Contains(body, "a guess:") {
		t.Error("the pane repeats the program the list beside it shows")
	}
}

func TestAddressNames(t *testing.T) {
	for addr, want := range map[string][]string{
		"widget-fixverify-1795485606@x.example": {"widget", "widgetfixverify"},
		"acme.corp.7@x.example":                 {"acme", "acmecorp"},
		"acme-1795@x.example":                   {"acme"},
		"@x.example":                            nil,
	} {
		if got := addressNames(addr); !slices.Equal(got, want) {
			t.Errorf("addressNames(%s) = %v", addr, got)
		}
	}
	for w, want := range map[string]bool{"widget": true, "news": false, "abc": false, "test": false} {
		if isName(w) != want {
			t.Errorf("isName(%s) = %v", w, !want)
		}
	}
}

// A message's links: content and action first, the sender's own folded
// under one line naming their kinds.
func TestMailLinks(t *testing.T) {
	v := &mailMessageView{mailRow: mailRow{Email: huntmail.Email{LinkDetails: []huntmail.Link{
		{URL: "https://a.example/job/1", Text: "A job"},
		{URL: "https://a.example/verify", Text: "Verify", Kind: "action"},
		{URL: "https://facebook.example/a", Text: "Facebook Logo", Kind: "social"},
		{URL: "https://a.example/privacy", Text: "Privacy", Kind: "legal"},
		{URL: "https://a.example/terms", Text: "Terms", Kind: "legal"},
		{URL: "https://a.example/unsub", Text: "Unsubscribe", Kind: "unsubscribe"},
		{URL: "https://apps.example/x", Text: "App Store", Kind: "app"},
		{URL: "https://a.example/new-kind", Text: "?", Kind: "something-new"},
	}}}}
	l := v.Links()
	if len(l.Main) != 2 || len(l.Other) != 6 || l.OtherKinds != "social, legal, unsubscribe, 2 more" {
		t.Errorf("links: %d main, %d other (%s)", len(l.Main), len(l.Other), l.OtherKinds)
	}
	if v.LinksOpen() {
		t.Error("the links start open though their action link has a box of its own above")
	}
	v.LinkDetails = l.Main[:1]
	if !v.LinksOpen() {
		t.Error("a single content link starts folded")
	}
}

// The HuntMail page lists the top senders, when HuntMail has them: a
// sender with newsletters can have them blocked from its row (and the page
// comes back to the table), one already blocked or without any says so.
func TestMailStatsSenders(t *testing.T) {
	mail := newFakeMail()
	blocks := []huntmail.Block{}
	mail.blocks = &blocks
	e := newTestEnv(t, func(d *Deps) { d.Mail = mail })
	c := e.client()
	if _, body := e.do(c, "GET", "/email/stats", nil, nil); strings.Contains(body, `id="mail-senders"`) {
		t.Error("a HuntMail without top senders shows them")
	}

	mail.senders = []huntmail.Sender{
		{Domain: "shop.example", Messages: 40, Newsletters: 38, Bytes: 2 << 20},
		{Domain: "news.example", Messages: 20, Newsletters: 20, Blocked: huntmail.BlockNewsletters, BlockID: new(int64(11))},
		{Domain: "loud.example", Messages: 9, Newsletters: 3, Blocked: huntmail.BlockAll, BlockID: new(int64(12))},
		{Domain: "old.example", Messages: 7, Newsletters: 7, Blocked: huntmail.BlockAll}, // from a HuntMail that doesn't say which block
		{Domain: "acme.example", Messages: 5},
	}
	_, body := e.do(c, "GET", "/email/stats", nil, nil)
	if mail.sendersAsked != [2]int{mailSenderDays, mailSenderRows} {
		t.Errorf("asked for %v", mail.sendersAsked)
	}
	row := func(domain string) string {
		i := strings.Index(body, `href="/email?q=`+domain+`"`)
		if i < 0 {
			t.Fatalf("no row for %s", domain)
		}
		return body[i:min(len(body), i+1200)]
	}
	for domain, want := range map[string]string{
		"shop.example": `<input type="hidden" name="value" value="shop.example"><input type="hidden" name="scope" value="newsletters"><input type="hidden" name="from" value="senders">`,
		"news.example": `newsletters blocked</a><form method="post" action="/email/blocks/11/delete" data-confirm="Unblock news.example?`,
		"loud.example": `all blocked</a><form method="post" action="/email/blocks/12/delete"`,
		"old.example":  "all blocked</a></div>",
		"acme.example": "no newsletters",
	} {
		r := row(domain)
		if end := strings.Index(r, "</tr>"); end >= 0 {
			r = r[:end]
		}
		if !strings.Contains(r, want) {
			t.Errorf("%s: no %q in\n%s", domain, want, r)
		}
		if domain != "shop.example" && strings.Contains(r, `action="/email/blocks"`) {
			t.Errorf("%s offers a block", domain)
		}
	}
	if !strings.Contains(body, "Top senders <span class=\"count\">5</span>") || !strings.Contains(body, "2.1 MB") {
		t.Error("the table's heading or sizes")
	}

	res, _ := e.do(c, "POST", "/email/blocks", url.Values{"kind": {"sender"}, "value": {"shop.example"}, "scope": {"newsletters"}, "from": {"senders"}}, nil)
	if loc := res.Header.Get("Location"); res.StatusCode != http.StatusSeeOther || !strings.HasSuffix(loc, "#mail-senders") {
		t.Errorf("blocking from the table goes to %d %q", res.StatusCode, loc)
	}
	if len(blocks) != 1 || blocks[0].Value != "shop.example" || blocks[0].Scope != huntmail.BlockNewsletters {
		t.Errorf("blocks: %+v", blocks)
	}

	// Unblocking from the table comes back to it.
	res, _ = e.do(c, "POST", "/email/blocks/"+strconv.FormatInt(blocks[0].ID, 10)+"/delete", url.Values{"from": {"senders"}}, nil)
	if loc := res.Header.Get("Location"); res.StatusCode != http.StatusSeeOther || !strings.HasSuffix(loc, "#mail-senders") || len(blocks) != 0 {
		t.Errorf("unblocking from the table: %d %q, %d left", res.StatusCode, loc, len(blocks))
	}

	// A HuntMail without blocks lists its senders without the button.
	mail.blocks = nil
	if _, body := e.do(c, "GET", "/email/stats", nil, nil); !strings.Contains(body, `id="mail-senders"`) || strings.Contains(body, `name="from" value="senders"`) {
		t.Error("without blocks: the table, without its buttons")
	}
}
