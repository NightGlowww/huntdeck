// Package huntmail reads a HuntMail inbox: the user's own catch-all for
// hunting, which runs on their Cloudflare account and receives the mail
// HuntDeck can't (https://github.com/mdpsec/huntmail). HuntDeck only reads
// it, through its API, from the server: the key never reaches a page.
//
// HuntMail's address holds a secret of its own (a random base path), so
// nothing here puts it in an error or a log: errors name the call, never
// the URL.
//
// Request budget: Cloudflare's free plan allows 100,000 Worker requests a
// day per account. HuntDeck makes one request per page the user opens
// (a list, a message, a program's mail), and none in the background.
package huntmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
)

// Errors a caller can tell apart.
var (
	// ErrNotConfigured: no HuntMail address or key is set.
	ErrNotConfigured = errors.New("huntmail: not set up")
	// ErrUnauthorized: HuntMail refused the key (HTTP 401).
	ErrUnauthorized = errors.New("huntmail: the API key was refused")
	// ErrNotFound: no such message, or (for every call) a wrong address:
	// HuntMail answers 404 outside its base path.
	ErrNotFound = errors.New("huntmail: not found")
)

// APIError is an answer other than success, 401, 403 or 404; Text is
// HuntMail's own reason ("" without one), which a 400 words for a person.
type APIError struct {
	Op     string
	Status int
	Text   string
}

func (e *APIError) Error() string {
	if e.Text == "" {
		return fmt.Sprintf("huntmail: %s: HTTP %d", e.Op, e.Status)
	}
	return fmt.Sprintf("huntmail: %s: HTTP %d: %s", e.Op, e.Status, e.Text)
}

// Email is one message, as HuntMail parsed it. Everything in it was
// written by whoever sent the mail.
type Email struct {
	ID           string       `json:"id"`
	ReceivedAt   time.Time    `json:"received_at"`
	To           string       `json:"to"`
	From         string       `json:"from"`
	HeaderFrom   string       `json:"header_from"`
	HeaderTo     string       `json:"header_to"`
	Subject      string       `json:"subject"`
	MessageID    string       `json:"message_id"`
	SentAt       *time.Time   `json:"sent_at"`
	Text         string       `json:"text"`
	HasHTML      bool         `json:"has_html"`
	Codes        []string     `json:"codes"`
	Links        []string     `json:"links"`
	LinkDetails  []Link       `json:"link_details"`
	Attachments  []Attachment `json:"attachments"`
	Auth         Auth         `json:"auth"`
	RawSize      int64        `json:"raw_size"`
	RawTruncated bool         `json:"raw_truncated"`
	// ParseStatus: ok, partial, pending, deferred or failed.
	ParseStatus string `json:"parse_status"`
	// Action is the link the mail is about (HuntMail 1.2 and later; nil
	// without one, or from older versions).
	Action *Action `json:"action"`
	// Newsletter: HuntMail took it for a newsletter (list headers, no code
	// or action link, not account mail). False until it's parsed, and
	// from versions that don't tell (Stats.Newsletters is nil there).
	Newsletter bool `json:"newsletter"`
	// SenderDomain is the From header's domain, as HuntMail's sender
	// blocks match it ("" without one, or from versions before blocks).
	SenderDomain string `json:"sender_domain"`
}

// Action is the link a mail is about: Kind is "Verify", "Reset", "Sign
// in" or "Invite".
type Action struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
	Text string `json:"text"`
}

// Link is a link in the mail and the text a reader sees for it.
type Link struct {
	URL  string `json:"url"`
	Text string `json:"text"`
	// Kind is what HuntMail took it for: "" for the mail's content,
	// "action" (the link the mail is about), or the sender's own links:
	// "unsubscribe", "view" (in a browser), "social", "app" (app stores),
	// "legal", "support", "image" (a logo or icon). Versions that don't
	// tell send none: every link is content then.
	Kind string `json:"kind"`
}

// LinkAction and LinkContent are Link.Kind's values for the links a reader
// wants; any other kind is the sender's own.
const (
	LinkAction  = "action"
	LinkContent = ""
)

// Attachment is one of a message's attachments, by its index.
type Attachment struct {
	Filename  string `json:"filename"`
	MimeType  string `json:"mime_type"`
	Size      int64  `json:"size"`
	ContentID string `json:"content_id"`
	Inline    bool   `json:"inline"`
}

// Auth is what Cloudflare recorded about the sender on arrival.
type Auth struct {
	SPF             string   `json:"spf"`
	DKIM            string   `json:"dkim"`
	DMARC           string   `json:"dmarc"`
	VerifiedDomains []string `json:"verified_domains"`
	SpamScore       *float64 `json:"spam_score"`
}

// Filter narrows a list. Zero values leave a filter out.
type Filter struct {
	To         string // exact recipient
	ToPrefix   string // recipients starting with this
	ToDomain   string // recipients on this domain (HuntMail 1.1 and later)
	Subject    string // subject contains
	Q          string // subject, sender or header recipient contains
	FromDomain string // only mail verified for this domain
	Since      time.Time
	Before     time.Time
	Limit      int    // 1 to 200; HuntMail's default (20) when 0
	Cursor     string // Page.NextCursor of the page before
	NoText     bool   // leave the bodies out
	// Newsletters is NewslettersHide or NewslettersOnly; "" lists them with
	// the rest. Versions that don't tell newsletters apart ignore it.
	Newsletters string
}

// Filter.Newsletters' values.
const (
	NewslettersHide = "hide"
	NewslettersOnly = "only"
)

// Page is a page of mail, newest first.
type Page struct {
	Emails     []Email `json:"emails"`
	NextCursor string  `json:"next_cursor"`
}

// Stats is the inbox's overview.
type Stats struct {
	Version        string     `json:"version"` // HuntMail's, from versions that say it
	Messages       int64      `json:"messages"`
	Today          int64      `json:"today"`
	StoredBytes    int64      `json:"stored_bytes"`
	MaxStoreBytes  int64      `json:"max_store_bytes"`
	RetentionDays  int        `json:"retention_days"`
	Oldest         *time.Time `json:"oldest"`
	Newest         *time.Time `json:"newest"`
	StoreFailures  int64      `json:"store_failures"`
	Domains        []Domain   `json:"domains"`
	DomainsSetUp   []string   `json:"domains_set_up"`
	ParsePending   int64      `json:"parse_pending"`
	ParseDeferred  int64      `json:"parse_deferred"` // big mail not read yet (HuntMail 1.1 and later)
	ParseFailed    int64      `json:"parse_failed"`
	LastStoreError *time.Time `json:"last_store_failure"`
	// Days are the mail per day (UTC), oldest first, today last; DomainDays
	// the same per recipient domain, days with mail only. Asked for with
	// Stats' days (HuntMail 1.1 and later).
	Days       []Day       `json:"days"`
	DomainDays []DomainDay `json:"domain_days"`
	// Newsletters is how many newsletters it holds; nil from versions that
	// don't tell them apart (so it's how to tell they can).
	Newsletters *int64 `json:"newsletters"`
	// Blocked is how much mail its blocks turned away; nil from versions
	// without blocks (so it's how to tell they have them).
	Blocked *int64 `json:"blocked"`
}

// Day is how much mail arrived on a day (UTC, "2006-01-02").
type Day struct {
	Day      string `json:"day"`
	Received int64  `json:"received"`
	RawBytes int64  `json:"raw_bytes"`
}

// DomainDay is how much mail a recipient domain got on a day.
type DomainDay struct {
	Day      string `json:"day"`
	Domain   string `json:"domain"`
	Received int64  `json:"received"`
}

// Domain is how much mail a recipient domain got in the last 30 days.
type Domain struct {
	Domain   string `json:"domain"`
	Received int64  `json:"received"`
	Today    int64  `json:"today"`
}

// ViewLink is a signed link to HuntMail's own rendered view of a message,
// openable without the key until it expires: a bearer credential.
type ViewLink struct {
	URL       string    `json:"url"`
	BodyURL   string    `json:"body_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// File is an attachment's bytes.
type File struct {
	Name        string
	ContentType string
	Body        []byte
}

// Client reads one HuntMail inbox.
type Client struct {
	base string // the HuntMail URL, without a trailing slash
	key  string
	http *httpclient.Client
}

// Options configures a Client.
type Options struct {
	URL   string // HUNTMAIL_URL: https://<worker>.<subdomain>.workers.dev/<base path>
	Key   string // HUNTMAIL_API_KEY
	Proxy *url.URL
	// HTTP replaces the HTTP client (tests).
	HTTP *httpclient.Client
}

// New returns a Client, or ErrNotConfigured without an address and key.
func New(o Options) (*Client, error) {
	base, err := CheckURL(o.URL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(o.Key) == "" {
		return nil, ErrNotConfigured
	}
	hc := o.HTTP
	if hc == nil {
		// Pages wait on these calls, so few retries and short waits.
		hc = httpclient.New(httpclient.Options{RatePerSecond: 5, Burst: 5, MaxAttempts: 3, Timeout: 20 * time.Second,
			RateLimitWait: 2 * time.Second, MaxBackoff: 10 * time.Second, MaxBodyBytes: 16 << 20, Proxy: o.Proxy})
	}
	return &Client{base: base, key: strings.TrimSpace(o.Key), http: hc}, nil
}

// CheckURL checks a HuntMail address and returns it without a trailing
// slash: https (http only for this machine), with a base path, no query.
func CheckURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", ErrNotConfigured
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "":
		return "", errors.New("huntmail: the address should look like https://<worker>.<subdomain>.workers.dev/<base path>")
	case u.Scheme != "https" && (u.Scheme != "http" || !localHost(u.Hostname())):
		return "", errors.New("huntmail: the address must start with https://")
	case strings.Trim(u.Path, "/") == "":
		return "", errors.New("huntmail: the address needs its base path (the part after the host that setup printed)")
	}
	return raw, nil
}

func localHost(h string) bool { return h == "localhost" || h == "127.0.0.1" || h == "::1" }

// ids are HuntMail's message ids; anything else never reaches a URL.
var idRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidID reports whether id can be a HuntMail message id.
func ValidID(id string) bool { return idRE.MatchString(id) }

// List returns a page of mail, newest first.
func (c *Client) List(ctx context.Context, f Filter) (Page, error) {
	q := url.Values{}
	set := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" {
			q.Set(k, v)
		}
	}
	set("to", f.To)
	set("to_prefix", f.ToPrefix)
	set("to_domain", f.ToDomain)
	set("subject", f.Subject)
	set("q", f.Q)
	set("from_domain", f.FromDomain)
	set("cursor", f.Cursor)
	if f.Newsletters == NewslettersHide || f.Newsletters == NewslettersOnly {
		q.Set("newsletters", f.Newsletters)
	}
	if !f.Since.IsZero() {
		q.Set("since", strconv.FormatInt(f.Since.UnixMilli(), 10))
	}
	if !f.Before.IsZero() {
		q.Set("before", strconv.FormatInt(f.Before.UnixMilli(), 10))
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(min(f.Limit, 200)))
	}
	if f.NoText {
		q.Set("text", "0")
	}
	var p Page
	err := c.getJSON(ctx, "list mail", "/api/emails", q, &p)
	return p, err
}

// Get returns one message.
func (c *Client) Get(ctx context.Context, id string) (Email, error) {
	var e Email
	if !ValidID(id) {
		return e, ErrNotFound
	}
	err := c.getJSON(ctx, "read a message", "/api/emails/"+id, nil, &e)
	return e, err
}

// View returns a signed link to HuntMail's rendered view of a message,
// with remote images when images is set.
func (c *Client) View(ctx context.Context, id string, images bool) (ViewLink, error) {
	var v ViewLink
	if !ValidID(id) {
		return v, ErrNotFound
	}
	q := url.Values{}
	if images {
		q.Set("images", "1")
	}
	err := c.getJSON(ctx, "make a view link", "/api/emails/"+id+"/view-link", q, &v)
	return v, err
}

// Attachment returns a message's attachment by its index.
func (c *Client) Attachment(ctx context.Context, id string, index int) (File, error) {
	if !ValidID(id) || index < 0 {
		return File{}, ErrNotFound
	}
	res, err := c.get(ctx, "download an attachment", "/api/emails/"+id+"/attachments/"+strconv.Itoa(index), nil)
	if err != nil {
		return File{}, err
	}
	f := File{ContentType: res.Header.Get("Content-Type"), Body: res.Body}
	if _, params, err := mime.ParseMediaType(res.Header.Get("Content-Disposition")); err == nil {
		f.Name = params["filename"]
	}
	return f, nil
}

// Stats reads the inbox's totals, with the last days days of daily counts
// when days is 1 to 400 (none when 0). It's cheap: HuntMail keeps
// running counters for it.
func (c *Client) Stats(ctx context.Context, days int) (Stats, error) {
	var q url.Values
	if days > 0 {
		q = url.Values{"days": {strconv.Itoa(min(days, 400))}}
	}
	var s Stats
	err := c.getJSON(ctx, "read the inbox's stats", "/api/stats", q, &s)
	return s, err
}

func (c *Client) getJSON(ctx context.Context, op, path string, q url.Values, v any) error {
	return c.callJSON(ctx, op, http.MethodGet, path, q, nil, v)
}

// callJSON sends a request (with in as its JSON body unless nil) and reads
// the JSON answer into v (unless nil).
func (c *Client) callJSON(ctx context.Context, op, method, path string, q url.Values, in, v any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return fmt.Errorf("huntmail: %s: %w", op, err)
		}
	}
	res, err := c.do(ctx, op, method, path, q, body)
	if err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	if err := json.Unmarshal(res.Body, v); err != nil {
		return fmt.Errorf("huntmail: %s: unexpected answer: %w", op, err)
	}
	return nil
}

func (c *Client) get(ctx context.Context, op, path string, q url.Values) (*httpclient.Response, error) {
	return c.do(ctx, op, http.MethodGet, path, q, nil)
}

// do sends a request, with body as JSON when there is one.
func (c *Client) do(ctx context.Context, op, method, path string, q url.Values, body []byte) (*httpclient.Response, error) {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body) // replayable: retries send it again
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, fmt.Errorf("huntmail: %s: %s", op, c.redact(err.Error()))
	}
	req.Header.Set("X-Api-Key", c.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(ctx, req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("huntmail: %s: %w", op, ctx.Err())
		}
		return nil, fmt.Errorf("huntmail: %s: %s", op, c.redact(err.Error()))
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return nil, ErrUnauthorized
	case res.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case res.StatusCode < 200 || res.StatusCode > 299:
		e := &APIError{Op: op, Status: res.StatusCode}
		var b struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(res.Body, &b) == nil && b.Error != "" && len(b.Error) < 200 {
			e.Text = c.redact(b.Error)
		}
		return nil, e
	}
	return res, nil
}

// redact takes HuntMail's address, its base path and the key out of a
// message, in any of the forms an error may quote them.
func (c *Client) redact(s string) string {
	secrets := []string{c.base, url.PathEscape(c.base), c.key}
	if u, err := url.Parse(c.base); err == nil {
		if p := strings.Trim(u.Path, "/"); p != "" {
			secrets = append(secrets, p, url.PathEscape(p))
		}
	}
	for _, x := range secrets {
		if x != "" {
			s = strings.ReplaceAll(s, x, "<huntmail>")
		}
	}
	return s
}
