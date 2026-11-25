package web

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/huntmail"
	"github.com/mdpsec/huntdeck/internal/store"
)

// The Email page: the user's HuntMail inbox (huntmail.go), read on the
// server so its key never reaches a page. Mail is untrusted input: it's
// shown as text (codes, links with their real address, the plain body),
// never as HTML; HuntMail's own locked-down view opens in a new tab.
// Never in a read-only copy.

// MailReader reads the user's HuntMail inbox (*huntmail.Client).
type MailReader interface {
	List(ctx context.Context, f huntmail.Filter) (huntmail.Page, error)
	Get(ctx context.Context, id string) (huntmail.Email, error)
	View(ctx context.Context, id string, images bool) (huntmail.ViewLink, error)
	Attachment(ctx context.Context, id string, index int) (huntmail.File, error)
	Stats(ctx context.Context, days int) (huntmail.Stats, error)
	Blocks(ctx context.Context) ([]huntmail.Block, error)
	AddBlock(ctx context.Context, b huntmail.NewBlock) (huntmail.Block, error)
	DeleteBlock(ctx context.Context, id int64) error
	Senders(ctx context.Context, days, limit int) ([]huntmail.Sender, error)
}

// mailPageSize is how many messages a page of mail lists, as Programs.
const mailPageSize = 30

// mailRow is a message in a list, with the program its address is for,
// or else the programs its sender's domain is in the scope of.
type mailRow struct {
	huntmail.Email
	Program *store.MailPrefix
	// Guess are the programs the mail is likeliest for, and GuessWhy why
	// (mailguess.go): a guess, as anyone can write any From.
	Guess    []store.ProgramRef
	GuessWhy string
}

// Code is the message's likeliest one-time code, "" without one.
func (m mailRow) Code() string {
	if len(m.Codes) > 0 {
		return m.Codes[0]
	}
	return ""
}

// Sender is who the mail says it's from: the From header's name or
// address, else the envelope sender.
func (m mailRow) Sender() string { return mailSender(m.Email) }

// Verified says the sender's domain passed DKIM or DMARC.
func (m mailRow) Verified() bool { return len(m.Auth.VerifiedDomains) > 0 }

func mailSender(e huntmail.Email) string {
	if f := strings.TrimSpace(e.HeaderFrom); f != "" {
		if name, _, ok := strings.Cut(f, "<"); ok && strings.TrimSpace(name) != "" {
			return strings.Trim(strings.TrimSpace(name), `"`)
		}
		return strings.Trim(f, "<> ")
	}
	return e.From
}

// mailTable is a list of mail as the shared table shows it (partials/
// mail-table.html), with the Program column or without.
type mailTable struct {
	Rows    []mailRow
	Program bool
	Pane    bool   // rows open in the Email page's pane on wide screens
	Open    string // the message open in it
	MarkNL  bool   // newsletters list with the rest: say which they are
}

// Table is a program's list, without the program.
func (v *programMailView) Table() mailTable {
	return mailTable{Rows: v.Rows, MarkNL: v.CanNL && v.NL == nlShow}
}

// fileSize is a size in bytes as a person reads it: "820 B", "34 KB", "2.1 MB", "9.4 GB".
func fileSize(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10) + " B"
	case n < 1000*1000:
		return strconv.FormatInt((n+500)/1000, 10) + " KB"
	case n < 1000*1000*1000:
		return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + " MB"
	}
	return strconv.FormatFloat(float64(n)/1e9, 'f', 1, 64) + " GB"
}

// programOfAddress finds the program an address was made for: the longest
// prefix (prefixes come longest first) it starts with, followed by a dash.
func programOfAddress(prefixes []store.MailPrefix, address string) *store.MailPrefix {
	local, _, _ := strings.Cut(strings.ToLower(address), "@")
	for i := range prefixes {
		if strings.HasPrefix(local, prefixes[i].Prefix+"-") {
			return &prefixes[i]
		}
	}
	return nil
}

func mailRows(emails []huntmail.Email, prefixes []store.MailPrefix) []mailRow {
	out := make([]mailRow, 0, len(emails))
	for _, e := range emails {
		out = append(out, mailRow{Email: e, Program: programOfAddress(prefixes, e.To)})
	}
	return out
}

// mailProblem says, in words a user can act on, why HuntMail didn't answer.
func mailProblem(err error) string {
	switch {
	case errors.Is(err, huntmail.ErrUnauthorized):
		return "HuntMail refused the API key. Check it in Email › HuntMail › Setup."
	case errors.Is(err, huntmail.ErrNotFound):
		return "HuntMail answered \"not found\". Check its address in Email › HuntMail › Setup, including the base path its setup printed."
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "HuntMail took too long to answer. Try again in a moment."
	}
	return "Couldn't read HuntMail: " + err.Error() + "."
}

type mailView struct {
	Configured bool
	CanSetUp   bool // HuntDeck can set HuntMail up itself
	Problem    string
	Rows       []mailRow
	Q, To      string
	Domain     string            // the recipient domain the list keeps to
	Domains    []string          // HuntMail's domains, for the filter
	Program    *store.MailPrefix // the program the list keeps to
	Programs   []store.MailPrefix
	// NL is which mail lists when HuntMail tells newsletters apart (CanNL):
	// "" all but newsletters, "show" all of it, "only" newsletters.
	NL     string
	CanNL  bool
	counts *mailCount // HuntMail's totals, when the list isn't filtered otherwise

	// Paging: HuntMail pages with a cursor that only goes older, so a
	// page's address carries the cursors of the pages before it (back).
	Size     int      // messages a page
	Sizes    []int    // the sizes to choose from
	Page     int      // 1 for the newest
	From     int      // the first row's position, from 1
	Total    int64    // every message, when the list isn't filtered; 0 if unknown
	cursor   string   // this page's
	back     []string // the pages before's, oldest first; "0" is the first page
	next     string   // the next (older) page's
	NextURL  string
	PrevURL  string
	FirstURL string
	Here     string // this page's address, to refresh it
	// Open is the message shown in the pane (?open=), nil for none.
	Open *mailMessageView
}

// Table is the inbox's list: with the program, unless it keeps to one,
// and the open message marked.
func (v *mailView) Table() mailTable {
	t := mailTable{Rows: v.Rows, Program: v.Program == nil, Pane: true, MarkNL: v.CanNL && v.NL == nlShow}
	if v.Open != nil {
		t.Open = v.Open.ID
	}
	return t
}

// mailPageSizes are the page sizes the Email page offers.
var mailPageSizes = []int{30, 50, 100}

// mailBackMax is how many earlier pages a page's address remembers: past
// it, Newer from the oldest it remembers goes to the first page.
const mailBackMax = 40

// Last is the row number of the page's last message.
func (v *mailView) Last() int { return v.From + len(v.Rows) - 1 }

// Filtered says the list doesn't show all the mail.
func (v *mailView) Filtered() bool {
	return v.Q != "" || v.To != "" || v.Domain != "" || v.Program != nil
}

// mailInbox serves the Email page.
func (s *server) mailInbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	v := mailView{Configured: s.Mail != nil, CanSetUp: s.MailBundle != nil && s.Accounts != nil, Q: strings.TrimSpace(q.Get("q")),
		To: strings.ToLower(strings.TrimSpace(q.Get("to"))), Domain: strings.ToLower(strings.TrimSpace(q.Get("domain"))),
		Size: mailPageSize, Sizes: mailPageSizes, cursor: q.Get("cursor")}
	v.NL = nlParam(q)
	if n, err := strconv.Atoi(q.Get("n")); err == nil && slices.Contains(mailPageSizes, n) {
		v.Size = n
	}
	if b := q.Get("back"); b != "" && v.cursor != "" {
		v.back = strings.Split(b, ",")
	}
	v.Page = 1
	if v.cursor != "" {
		v.Page = len(v.back) + 1
	}
	v.From = (v.Page-1)*v.Size + 1
	v.Here = r.URL.RequestURI()
	page := pageData{Title: "Email", Nav: "email", Data: &v}
	if s.Mail == nil {
		s.render(w, r, http.StatusOK, "email", page)
		return
	}
	prefixes, err := s.Store.MailPrefixes(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Programs = prefixes
	f := huntmail.Filter{Q: v.Q, To: v.To, ToDomain: v.Domain, Cursor: v.cursor, Limit: v.Size, NoText: true}
	if id, err := strconv.ParseInt(q.Get("program"), 10, 64); err == nil {
		for i := range prefixes {
			if prefixes[i].ProgramID == id {
				v.Program = &prefixes[i]
			}
		}
		if v.Program == nil {
			s.renderError(w, r, http.StatusNotFound, "That program has no addresses.")
			return
		}
		f.ToPrefix, f.To = v.Program.Prefix+"-", ""
	}
	if v.Domains, err = s.mailDomains(ctx); err != nil || !s.mailByDomain() {
		v.Domains = nil // the filter just isn't offered
		if v.Domain != "" {
			v.Problem = "This HuntMail can't keep to a domain: update it in Email › HuntMail › Setup."
			s.render(w, r, http.StatusOK, "email", page)
			return
		}
	}
	if v.Domain != "" && !slices.Contains(v.Domains, v.Domain) {
		v.Domains = append(v.Domains, v.Domain)
	}
	if v.CanNL = s.mailTellsNewsletters(); v.CanNL {
		f.Newsletters = nlFilter(v.NL)
	} else {
		v.NL = ""
	}
	listCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	p, err := s.Mail.List(listCtx, f)
	if err != nil {
		v.Problem = mailProblem(err)
		s.render(w, r, http.StatusOK, "email", page)
		return
	}
	v.Rows = mailRows(p.Emails, prefixes)
	s.guessPrograms(ctx, v.Rows)
	if !v.Filtered() {
		if n, err := s.mailTotals(ctx); err == nil {
			v.counts, v.Total = &n, n.All
			if v.CanNL {
				v.Total = n.of(v.NL)
			}
		}
	}
	v.next = p.NextCursor
	v.pager()
	if id := q.Get("open"); huntmail.ValidID(id) {
		open, found, err := s.loadMail(ctx, id)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if !found {
			open = &mailMessageView{Problem: mailGone}
		}
		open.Pane, v.Open = true, open
	}
	s.render(w, r, http.StatusOK, "email", page)
}

// The Email page's newsletter views (nl=), besides the default: all but
// newsletters.
const (
	nlShow = "show"
	nlOnly = "only"
)

// nlFilter is HuntMail's newsletters filter for a view.
func nlFilter(nl string) string {
	switch nl {
	case nlShow:
		return ""
	case nlOnly:
		return huntmail.NewslettersOnly
	}
	return huntmail.NewslettersHide
}

// of is how much mail a newsletter view lists.
func (n mailCount) of(nl string) int64 {
	switch nl {
	case nlShow:
		return n.All
	case nlOnly:
		return n.Newsletters
	}
	return n.All - n.Newsletters
}

// nlSegment is one of the newsletter toggle's views.
type nlSegment struct {
	Label, Title, URL string
	Count             int64
	Counted           bool
	On                bool
}

// nlSegments are the newsletter toggle's views: Mail (the default, no
// newsletters), Newsletters and All, cur on, each at link(its nl=), with
// HuntMail's counts when there are any.
func nlSegments(cur string, link func(nl string) string, counts *mailCount) []nlSegment {
	segs := []nlSegment{
		{Label: "Mail", Title: "Everything but newsletters: what HuntMail took for one has list headers and no code or link to act on"},
		{Label: "Newsletters", Title: "Only what HuntMail took for a newsletter"},
		{Label: "All", Title: "Every message, newsletters too"},
	}
	for i, nl := range []string{"", nlOnly, nlShow} {
		segs[i].URL, segs[i].On = link(nl), cur == nl
		if counts != nil {
			segs[i].Counted, segs[i].Count = true, counts.of(nl)
		}
	}
	return segs
}

// NLSegments are the Email page's newsletter toggle, counted when the list
// isn't filtered otherwise.
func (v *mailView) NLSegments() []nlSegment {
	return nlSegments(v.NL, func(nl string) string { return v.FilterOn("nl", nl) }, v.counts)
}

// nlParam is a newsletter view asked for in a page's address (nl=).
func nlParam(q url.Values) string {
	if nl := q.Get("nl"); nl == nlShow || nl == nlOnly {
		return nl
	}
	return ""
}

type mailMessageView struct {
	mailRow
	Problem string
	Pane    bool // in the Email page's pane, not on a page of its own
	// CanBlock: HuntMail has blocks, so the message offers to block its
	// sender or address (mailblocks.go).
	CanBlock bool
}

// loadMail reads one message for its page or the pane; a missing one is
// nil with found false.
func (s *server) loadMail(ctx context.Context, id string) (v *mailMessageView, found bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	e, err := s.Mail.Get(ctx, id)
	if errors.Is(err, huntmail.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return &mailMessageView{Problem: mailProblem(err)}, true, nil
	}
	prefixes, err := s.Store.MailPrefixes(ctx)
	if err != nil {
		return nil, false, err
	}
	rows := []mailRow{{Email: e, Program: programOfAddress(prefixes, e.To)}}
	s.guessPrograms(ctx, rows)
	return &mailMessageView{mailRow: rows[0], CanBlock: s.mailCanBlock(ctx)}, true, nil
}

const mailGone = "No such message: HuntMail may have let it go (it keeps mail for a while, then the oldest goes)."

// mailMessage serves one message on a page of its own.
func (s *server) mailMessage(w http.ResponseWriter, r *http.Request) {
	if s.Mail == nil {
		s.renderError(w, r, http.StatusNotFound, "HuntMail isn't set up.")
		return
	}
	v, found, err := s.loadMail(r.Context(), r.PathValue("id"))
	switch {
	case err != nil:
		s.serverError(w, r, err)
		return
	case !found:
		s.renderError(w, r, http.StatusNotFound, mailGone)
		return
	}
	title := strings.TrimSpace(v.Subject)
	if title == "" {
		title = "(no subject)"
	}
	s.render(w, r, http.StatusOK, "emailmsg", pageData{Title: title, Nav: "email", Crumbs: []crumb{{"Email", "/email"}}, Data: v})
}

// mailPane serves one message for the Email page's pane (mailpane.js).
func (s *server) mailPane(w http.ResponseWriter, r *http.Request) {
	if s.Mail == nil {
		http.NotFound(w, r)
		return
	}
	v, found, err := s.loadMail(r.Context(), r.PathValue("id"))
	switch {
	case err != nil:
		s.serverError(w, r, err)
		return
	case !found:
		v = &mailMessageView{Problem: mailGone}
	}
	v.Pane = true
	s.fragment(w, r, "email", "mail-pane", v)
}

// mailOpen sends the browser to HuntMail's own rendered view of a message,
// through a signed link that expires: HuntDeck never renders mail HTML.
func (s *server) mailOpen(w http.ResponseWriter, r *http.Request) {
	if s.Mail == nil {
		s.renderError(w, r, http.StatusNotFound, "HuntMail isn't set up.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	v, err := s.Mail.View(ctx, r.PathValue("id"), r.URL.Query().Get("images") == "1")
	if errors.Is(err, huntmail.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "No such message.")
		return
	}
	if err != nil {
		s.renderError(w, r, http.StatusBadGateway, mailProblem(err))
		return
	}
	if u, err := url.Parse(v.URL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		s.renderError(w, r, http.StatusBadGateway, "HuntMail's view link wasn't a web address.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, v.URL, http.StatusSeeOther) //nolint:gosec // HuntMail's own signed view link, checked to be http(s)
}

// mailAttachment downloads an attachment, as a file: never shown inline,
// whatever the mail says it is.
func (s *server) mailAttachment(w http.ResponseWriter, r *http.Request) {
	if s.Mail == nil {
		s.renderError(w, r, http.StatusNotFound, "HuntMail isn't set up.")
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 0 {
		s.renderError(w, r, http.StatusNotFound, "No such attachment.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	f, err := s.Mail.Attachment(ctx, r.PathValue("id"), n)
	if errors.Is(err, huntmail.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "No such attachment.")
		return
	}
	if err != nil {
		s.renderError(w, r, http.StatusBadGateway, mailProblem(err))
		return
	}
	name := strings.Map(func(c rune) rune {
		if c < 0x20 || c == 0x7f || strings.ContainsRune(`/\"`, c) {
			return '_'
		}
		return c
	}, f.Name)
	if strings.Trim(name, ". ") == "" {
		name = "attachment"
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Length", strconv.Itoa(len(f.Body)))
	_, _ = w.Write(f.Body)
}
