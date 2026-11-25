package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/huntmail"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// HuntMail's blocks (huntmail/blocks.go), which it keeps and applies: mail
// they match is turned away on arrival, never stored. HuntDeck lists them
// on the HuntMail page (mailstats.go), adds them (there, or from a
// message's Block menu) and removes them; it keeps no list of its own.

// mailBlockList is the blocks table (partials/mail-blocks.html) and the
// one just added.
type mailBlockList struct {
	Rows  []mailBlockRow
	Added int64
}

// mailBlockRow is a block with the program a prefix block is for.
type mailBlockRow struct {
	huntmail.Block
	Program *store.MailPrefix
}

// What says what a block turns away, in words.
func (b mailBlockRow) What() string {
	switch b.Kind {
	case huntmail.BlockSender:
		if b.Scope == huntmail.BlockNewsletters {
			return "Newsletters from"
		}
		return "All mail from"
	case huntmail.BlockRecipient:
		return "All mail to"
	case huntmail.BlockPrefix:
		return "All mail to addresses starting"
	}
	return b.Kind
}

// OtherScope is the scope a sender block can switch to, "" for the others.
func (b mailBlockRow) OtherScope() string {
	switch {
	case b.Kind != huntmail.BlockSender:
		return ""
	case b.Scope == huntmail.BlockNewsletters:
		return huntmail.BlockAll
	}
	return huntmail.BlockNewsletters
}

// mailBlocks was Email › Blocks, now a part of the HuntMail page.
func (s *server) mailBlocks(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/email/stats#mail-blocks", http.StatusSeeOther)
}

// mailBlockRows are HuntMail's blocks, each prefix block with its program.
func (s *server) mailBlockRows(ctx context.Context, prefixes []store.MailPrefix) ([]mailBlockRow, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	blocks, err := s.Mail.Blocks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]mailBlockRow, 0, len(blocks))
	for _, b := range blocks {
		row := mailBlockRow{Block: b}
		if b.Kind == huntmail.BlockPrefix {
			if p := programOfAddress(prefixes, b.Value); p != nil && b.Value == p.Prefix+"-" {
				row.Program = p
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// mailBlockAdd adds a block (or changes a sender block's scope): from the
// HuntMail page or a message. HuntMail checks it; a refusal shows its reason.
func (s *server) mailBlockAdd(w http.ResponseWriter, r *http.Request) {
	if s.Mail == nil {
		s.renderError(w, r, http.StatusNotFound, "HuntMail isn't set up.")
		return
	}
	b := huntmail.NewBlock{Kind: r.PostFormValue("kind"), Value: strings.TrimSpace(r.PostFormValue("value")), Scope: r.PostFormValue("scope")}
	switch b.Kind {
	case huntmail.BlockSender:
		if b.Scope != huntmail.BlockAll {
			b.Scope = huntmail.BlockNewsletters
		}
	case huntmail.BlockRecipient, huntmail.BlockPrefix:
		b.Scope = huntmail.BlockAll
	default:
		s.renderError(w, r, http.StatusBadRequest, "Block a sender, an address or addresses starting with something.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	added, err := s.Mail.AddBlock(ctx, b)
	var refused *huntmail.APIError
	switch {
	case errors.As(err, &refused) && refused.Status == http.StatusBadRequest:
		why := refused.Text
		if why == "" {
			why = "it isn't a block HuntMail can keep"
		}
		s.renderMailStats(w, r, http.StatusBadRequest, "HuntMail didn't add it: "+why+".", b)
		return
	case err != nil:
		s.renderMailStats(w, r, http.StatusBadGateway, mailProblem(err), b)
		return
	}
	id := strconv.FormatInt(added.ID, 10)
	if r.PostFormValue("from") == "senders" { // back to the row it was blocked from
		http.Redirect(w, r, "/email/stats?added="+id+"#mail-senders", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/email/stats?added="+id+"#block-"+id, http.StatusSeeOther)
}

// mailBlockDelete removes a block.
func (s *server) mailBlockDelete(w http.ResponseWriter, r *http.Request) {
	if s.Mail == nil {
		s.renderError(w, r, http.StatusNotFound, "HuntMail isn't set up.")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.renderError(w, r, http.StatusNotFound, "No such block.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.Mail.DeleteBlock(ctx, id); err != nil && !errors.Is(err, huntmail.ErrNotFound) { // gone already is done
		s.renderError(w, r, http.StatusBadGateway, mailProblem(err))
		return
	}
	if r.PostFormValue("from") == "senders" { // back to the row it was unblocked from
		http.Redirect(w, r, "/email/stats#mail-senders", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/email/stats#mail-blocks", http.StatusSeeOther)
}

// BlockDomains are the domains a message's sender can be blocked by: the
// one it's from, and its root domain when that's another (news.shop.example,
// shop.example), as HuntMail matches them.
func (m *mailMessageView) BlockDomains() []string {
	d := strings.ToLower(strings.TrimSpace(m.SenderDomain))
	if d == "" {
		return nil
	}
	out := []string{d}
	if root := scope.RootDomain(d); root != "" && root != d {
		out = append(out, root)
	}
	return out
}
