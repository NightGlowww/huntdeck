package huntmail

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
)

const (
	testKey  = "0123456789abcdef0123456789abcdef-test-key"
	basePath = "s3cr3tb4s3p4th00"
)

// fakeHuntMail replays the fixtures under /<basePath>/, needs the key, and
// answers 404 elsewhere, as HuntMail does.
func fakeHuntMail(t *testing.T, handle func(w http.ResponseWriter, r *http.Request) bool) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handle != nil && handle(w, r) {
			return
		}
		path, ok := strings.CutPrefix(r.URL.Path, "/"+basePath)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Api-Key") != testKey {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		file := map[string]string{
			"/api/emails": "list.json",
			"/api/emails/019a8e4f3c2b7d1e9f0a4b6c8d2e":           "email.json",
			"/api/emails/019a8e4f3c2b7d1e9f0a4b6c8d2e/view-link": "viewlink.json",
			"/api/stats": "stats.json",
		}[path]
		if strings.HasSuffix(path, "/attachments/0") {
			w.Header().Set("Content-Type", "application/pdf")
			w.Header().Set("Content-Disposition", `attachment; filename="terms.pdf"`)
			_, _ = w.Write([]byte("%PDF-1.4 test"))
			return
		}
		if file == "" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		b, err := os.ReadFile("testdata/" + file)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	c, err := New(Options{URL: srv.URL + "/" + basePath + "/", Key: testKey, HTTP: testHTTP()})
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func testHTTP() *httpclient.Client {
	return httpclient.New(httpclient.Options{RatePerSecond: 1000, Burst: 100, MaxAttempts: 3,
		Sleep: func(context.Context, time.Duration) error { return nil }})
}

func TestListGetStats(t *testing.T) {
	var query atomic.Value
	c, _ := fakeHuntMail(t, func(_ http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/api/emails") {
			query.Store(r.URL.RawQuery)
		}
		return false
	})
	ctx := context.Background()
	since := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	p, err := c.List(ctx, Filter{ToPrefix: "acme-", Q: "code", Since: since, Limit: 500, NoText: true, Cursor: "c1", Newsletters: NewslettersHide})
	if err != nil {
		t.Fatal(err)
	}
	if got := query.Load().(string); got != "cursor=c1&limit=200&newsletters=hide&q=code&since=1793491200000&text=0&to_prefix=acme-" {
		t.Errorf("query = %s", got)
	}
	if len(p.Emails) != 2 || p.NextCursor != "c2" || p.Emails[0].Codes[0] != "482913" ||
		p.Emails[0].LinkDetails[0].Text != "Verify your email" || p.Emails[1].SentAt != nil || p.Emails[1].ParseStatus != "deferred" ||
		p.Emails[0].Auth.VerifiedDomains[0] != "target.example" || p.Emails[1].Auth.SpamScore != nil ||
		!p.Emails[0].Newsletter || p.Emails[1].Newsletter {
		t.Errorf("page: %+v", p)
	}
	e, err := c.Get(ctx, "019a8e4f3c2b7d1e9f0a4b6c8d2e")
	if err != nil || !strings.Contains(e.Text, "482913") || e.ReceivedAt.IsZero() {
		t.Errorf("get: %+v %v", e, err)
	}
	s, err := c.Stats(ctx, 0)
	if err != nil || s.Messages != 1234 || len(s.DomainsSetUp) != 2 || s.Domains[0].Domain != "example.com" || s.Newest == nil ||
		s.Newsletters == nil || *s.Newsletters != 56 {
		t.Errorf("stats: %+v %v", s, err)
	}
	v, err := c.View(ctx, "019a8e4f3c2b7d1e9f0a4b6c8d2e", true)
	if err != nil || !strings.Contains(v.URL, "/view/") || v.ExpiresAt.IsZero() {
		t.Errorf("view: %+v %v", v, err)
	}
	f, err := c.Attachment(ctx, "019a8e4f3c2b7d1e9f0a4b6c8d2e", 0)
	if err != nil || f.Name != "terms.pdf" || f.ContentType != "application/pdf" || len(f.Body) == 0 {
		t.Errorf("attachment: %+v %v", f, err)
	}
}

// Errors say what failed, never HuntMail's address, base path or key.
func TestErrors(t *testing.T) {
	calls := atomic.Int32{}
	c, srv := fakeHuntMail(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case strings.Contains(r.URL.RawQuery, "q=busy"):
			if calls.Add(1) == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return true
			}
		case strings.Contains(r.URL.RawQuery, "q=broken"):
			w.WriteHeader(http.StatusBadGateway)
			return true
		case strings.Contains(r.URL.RawQuery, "q=bad"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"to_prefix must be 3 to 254 characters"}`))
			return true
		}
		return false
	})
	ctx := context.Background()
	if _, err := c.List(ctx, Filter{Q: "busy"}); err != nil {
		t.Errorf("a 429 then an answer: %v", err)
	}
	cases := []struct {
		name string
		err  error
		want error
		text string
	}{
		{"wrong key", func() error {
			w, _ := New(Options{URL: srv.URL + "/" + basePath, Key: "wrong", HTTP: testHTTP()})
			_, err := w.Stats(ctx, 0)
			return err
		}(), ErrUnauthorized, ""},
		{"wrong base path", func() error {
			w, _ := New(Options{URL: srv.URL + "/nope-nope-nope-nope", Key: testKey, HTTP: testHTTP()})
			_, err := w.Stats(ctx, 0)
			return err
		}(), ErrNotFound, ""},
		{"no such message", func() error { _, err := c.Get(ctx, "nosuchmessage"); return err }(), ErrNotFound, ""},
		{"not an id", func() error { _, err := c.Get(ctx, "../stats"); return err }(), ErrNotFound, ""},
		{"server error", func() error { _, err := c.List(ctx, Filter{Q: "broken"}); return err }(), nil, "HTTP 502"},
		{"bad request", func() error { _, err := c.List(ctx, Filter{Q: "bad"}); return err }(), nil, "to_prefix must be"},
		{"unreachable", func() error {
			w, _ := New(Options{URL: "http://127.0.0.1:1/" + basePath, Key: testKey, HTTP: testHTTP()})
			_, err := w.Stats(ctx, 0)
			return err
		}(), nil, "huntmail: read the inbox's stats"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				t.Fatal("no error")
			}
			if tc.want != nil && !errors.Is(tc.err, tc.want) {
				t.Errorf("err = %v, want %v", tc.err, tc.want)
			}
			if tc.text != "" && !strings.Contains(tc.err.Error(), tc.text) {
				t.Errorf("err = %v, want it to say %q", tc.err, tc.text)
			}
			if strings.Contains(tc.err.Error(), basePath) || strings.Contains(tc.err.Error(), testKey) {
				t.Errorf("err gives away a secret: %v", tc.err)
			}
		})
	}
}

func TestCheckURL(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"https://huntmail.example.workers.dev/" + basePath + "/", "https://huntmail.example.workers.dev/" + basePath, true},
		{"  https://mail.example.com/" + basePath, "https://mail.example.com/" + basePath, true},
		{"http://127.0.0.1:8787/" + basePath, "http://127.0.0.1:8787/" + basePath, true},
		{"http://mail.example.com/" + basePath, "", false},
		{"https://mail.example.com", "", false},
		{"https://mail.example.com/", "", false},
		{"https://user:pw@mail.example.com/" + basePath, "", false},
		{"https://mail.example.com/" + basePath + "?x=1", "", false},
		{"mail.example.com/" + basePath, "", false},
		{"", "", false},
	} {
		got, err := CheckURL(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("CheckURL(%q) = %q, %v", tc.in, got, err)
		}
	}
	if _, err := New(Options{URL: "https://mail.example.com/" + basePath}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("no key: %v", err)
	}
}

// Blocks: listed, added as JSON with the key, refused with HuntMail's
// reason, removed by id.
func TestBlocks(t *testing.T) {
	var got atomic.Value
	c, _ := fakeHuntMail(t, func(w http.ResponseWriter, r *http.Request) bool {
		path, _ := strings.CutPrefix(r.URL.Path, "/"+basePath)
		if !strings.HasPrefix(path, "/api/blocks") {
			return false
		}
		if r.Header.Get("X-Api-Key") != testKey {
			w.WriteHeader(http.StatusUnauthorized)
			return true
		}
		body, _ := io.ReadAll(r.Body)
		got.Store(r.Method + " " + path + " " + r.Header.Get("Content-Type") + " " + string(body))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"blocks":[{"id":7,"kind":"sender","value":"shop.example","scope":"newsletters","created_at":"2026-11-20T10:00:00Z","hits":3,"last_hit":"2026-11-22T10:00:00Z"}]}`))
		case r.Method == http.MethodPost && strings.Contains(string(body), `"bad"`):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"a sender block takes a domain name"}`))
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":8,"kind":"sender","value":"news.example","scope":"all","created_at":"2026-11-23T10:00:00Z","hits":0,"last_hit":null}`))
		case r.Method == http.MethodDelete && path == "/api/blocks/8":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
		return true
	})
	ctx := context.Background()
	bs, err := c.Blocks(ctx)
	if err != nil || len(bs) != 1 || bs[0].ID != 7 || bs[0].Hits != 3 || bs[0].LastHit == nil || bs[0].Scope != BlockNewsletters {
		t.Fatalf("blocks: %+v %v", bs, err)
	}
	b, err := c.AddBlock(ctx, NewBlock{Kind: BlockSender, Value: "news.example", Scope: BlockAll})
	if err != nil || b.ID != 8 || b.LastHit != nil {
		t.Errorf("add: %+v %v", b, err)
	}
	if g := got.Load().(string); g != `POST /api/blocks application/json {"kind":"sender","value":"news.example","scope":"all"}` {
		t.Errorf("add sent %s", g)
	}
	var ae *APIError
	if _, err := c.AddBlock(ctx, NewBlock{Kind: BlockSender, Value: "bad"}); !errors.As(err, &ae) || ae.Status != 400 || ae.Text != "a sender block takes a domain name" {
		t.Errorf("a refused block: %v", err)
	}
	if err := c.DeleteBlock(ctx, 8); err != nil {
		t.Errorf("delete: %v", err)
	}
	if err := c.DeleteBlock(ctx, 9); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete a missing block: %v", err)
	}
}

// Senders asks for the window and the number wanted, kept to what HuntMail
// takes, and reads a block's scope (null when there's none); an older
// HuntMail, without the route, answers ErrNotFound.
func TestSenders(t *testing.T) {
	var got atomic.Value
	old := atomic.Bool{}
	c, _ := fakeHuntMail(t, func(w http.ResponseWriter, r *http.Request) bool {
		path, _ := strings.CutPrefix(r.URL.Path, "/"+basePath)
		if path != "/api/senders" {
			return false
		}
		got.Store(r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		if old.Load() {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return true
		}
		_, _ = w.Write([]byte(`{"days":7,"senders":[{"domain":"shop.example","messages":40,"newsletters":38,"bytes":2048000,"blocked":"newsletters","block_id":7},` +
			`{"domain":"acme.example","messages":12,"newsletters":0,"bytes":51200,"blocked":null}]}`))
		return true
	})
	ctx := context.Background()
	ss, err := c.Senders(ctx, 7, 25)
	if err != nil || len(ss) != 2 || ss[0].Domain != "shop.example" || ss[0].Blocked != BlockNewsletters || ss[0].BlockID == nil || *ss[0].BlockID != 7 || ss[1].BlockID != nil || ss[0].Newsletters != 38 || ss[1].Blocked != "" || ss[1].Bytes != 51200 {
		t.Fatalf("senders: %+v %v", ss, err)
	}
	if q := got.Load().(string); q != "days=7&limit=25" {
		t.Errorf("query %q", q)
	}
	for _, tt := range []struct {
		days, limit int
		want        string
	}{{0, 0, "days=1&limit=1"}, {999, 500, "days=400&limit=100"}} {
		_, _ = c.Senders(ctx, tt.days, tt.limit)
		if q := got.Load().(string); q != tt.want {
			t.Errorf("%d, %d: query %q, want %q", tt.days, tt.limit, q, tt.want)
		}
	}
	old.Store(true)
	if _, err := c.Senders(ctx, 7, 25); !errors.Is(err, ErrNotFound) {
		t.Errorf("an older HuntMail: %v", err)
	}
}
