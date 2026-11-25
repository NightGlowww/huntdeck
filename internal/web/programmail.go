package web

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/huntmail"
	"github.com/mdpsec/huntdeck/internal/store"
)

// A program's Email tab: the HuntMail addresses made for it and the mail
// they got. A program gets one prefix (store.MailSlug, numbered if taken),
// and every address is "<prefix>-<unix time>-<5 random>@<domain>": the
// random part is what keeps other senders out, and "<prefix>-" finds the
// program's mail, also on addresses the user made by hand that way.

type programMailView struct {
	Prefix    string
	Addresses []store.MailAddress
	Domains   []string
	Rows      []mailRow
	Problem   string
	MoreURL   string // the rest on the Email page
	Made      int64  // the address just made, to point out
	// NL is which of its mail lists, as on the Email page (nl=), when
	// HuntMail tells newsletters apart (CanNL).
	NL    string
	CanNL bool
	tab   string // the tab's address
}

// NLSegments are the tab's newsletter toggle.
func (v *programMailView) NLSegments() []nlSegment {
	return nlSegments(v.NL, func(nl string) string {
		if nl == "" {
			return v.tab + "#mail-list"
		}
		return v.tab + "&nl=" + nl + "#mail-list"
	}, nil)
}

// mailTabShown is how much of a program's mail its tab lists.
const mailTabShown = 25

// programMail gathers the Email tab.
func (s *server) programMail(ctx context.Context, p *store.ProgramDetail, made int64, nl string) (*programMailView, error) {
	v := &programMailView{Made: made, NL: nl, tab: programPath(p) + "?tab=email"}
	var err error
	if v.Prefix, err = s.Store.MailPrefixOf(ctx, p.ID); err != nil {
		return nil, err
	}
	if v.Addresses, err = s.Store.MailAddresses(ctx, p.ID); err != nil {
		return nil, err
	}
	if v.Domains, err = s.mailDomains(ctx); err != nil {
		v.Problem = mailProblem(err)
		return v, nil
	}
	if v.Prefix == "" {
		return v, nil
	}
	listCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	f := huntmail.Filter{ToPrefix: v.Prefix + "-", Limit: mailTabShown, NoText: true}
	if v.CanNL = s.mailTellsNewsletters(); v.CanNL {
		f.Newsletters = nlFilter(v.NL)
	} else {
		v.NL = ""
	}
	page, err := s.Mail.List(listCtx, f)
	if err != nil {
		v.Problem = mailProblem(err)
		return v, nil
	}
	v.Rows = mailRows(page.Emails, nil)
	if page.NextCursor != "" {
		v.MoreURL = "/email?program=" + strconv.FormatInt(p.ID, 10)
		if v.NL != "" {
			v.MoreURL += "&nl=" + v.NL
		}
	}
	return v, nil
}

// newMailAddress makes an address for the program on the domain asked for.
func (s *server) newMailAddress(w http.ResponseWriter, r *http.Request) {
	p, ok := s.labelTarget(w, r)
	if !ok {
		return
	}
	if s.Mail == nil {
		s.renderError(w, r, http.StatusNotFound, "HuntMail isn't set up: add it in Email › HuntMail › Setup.")
		return
	}
	domains, err := s.mailDomains(r.Context())
	if err != nil {
		s.renderError(w, r, http.StatusBadGateway, mailProblem(err))
		return
	}
	domain := strings.ToLower(strings.TrimSpace(r.PostFormValue("domain")))
	if domain == "" && len(domains) > 0 {
		domain = domains[0]
	}
	if !slices.Contains(domains, domain) {
		s.renderError(w, r, http.StatusBadRequest, "Choose one of HuntMail's domains.")
		return
	}
	prefix, err := s.Store.EnsureMailPrefix(r.Context(), p.ID, store.MailSlug(p.Name))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var a store.MailAddress
	for range 3 { // a clash of the random part is all but impossible; try again if it happens
		a, err = s.Store.AddMailAddress(r.Context(), p.ID, mailAddress(prefix, s.Store.Now(), domain), r.PostFormValue("note"))
		if !errors.Is(err, store.ErrMailAddressTaken) {
			break
		}
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, programPath(p)+"?tab=email&made="+strconv.FormatInt(a.ID, 10)+"#mail-addresses", http.StatusSeeOther) //nolint:gosec // a local path
}

// mailAddress is "<prefix>-<unix time>-<5 random letters and digits>@domain".
func mailAddress(prefix string, now time.Time, domain string) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return prefix + "-" + strconv.FormatInt(now.Unix(), 10) + "-" + string(b) + "@" + domain
}

// noteMailAddress changes what one of the program's addresses is for.
func (s *server) noteMailAddress(w http.ResponseWriter, r *http.Request) {
	s.mailAddressAction(w, r, func(programID, id int64) error {
		return s.Store.SetMailAddressNote(r.Context(), programID, id, r.PostFormValue("note"))
	})
}

// forgetMailAddress drops one of the program's addresses from its list.
func (s *server) forgetMailAddress(w http.ResponseWriter, r *http.Request) {
	s.mailAddressAction(w, r, func(programID, id int64) error {
		return s.Store.DeleteMailAddress(r.Context(), programID, id)
	})
}

func (s *server) mailAddressAction(w http.ResponseWriter, r *http.Request, do func(programID, id int64) error) {
	p, ok := s.labelTarget(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.renderError(w, r, http.StatusNotFound, "No such address.")
		return
	}
	err = do(p.ID, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.renderError(w, r, http.StatusNotFound, "No such address.")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, programPath(p)+"?tab=email#mail-addresses", http.StatusSeeOther) //nolint:gosec // a local path
}
