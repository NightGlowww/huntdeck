package web

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/mdpsec/huntdeck/internal/huntmail"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Email › HuntMail: HuntMail's own numbers, as its stats page shows them
// (how much mail it holds and how full it is, mail per day, mail by
// domain, its top senders), and its blocks (mailblocks.go), to add,
// switch and unblock.

// mailStatsDays is how many days the chart and the domains cover.
const mailStatsDays = 30

// mailSenderDays and mailSenderRows are the top senders' window and how
// many it lists. HuntMail reads every message in the window for them, so
// the page asks once as it opens.
const (
	mailSenderDays = 7
	mailSenderRows = 25
)

type mailStatsView struct {
	Configured bool
	Problem    string
	S          huntmail.Stats
	StoredPct  float64 // of the cap, 0–100
	Chart      barChart
	HasDays    bool // HuntMail sent daily counts (1.1 and later)
	Domains    []mailDomainRow
	Backlog    int64 // mail not looked through for codes yet: small mail waiting, big mail deferred
	CanUpdate  bool  // HuntDeck has a newer HuntMail to set up
	// Newsletters is how many it holds, when it tells them apart (HasNL).
	Newsletters int64
	HasNL       bool
	// Blocked is how much mail its blocks turned away, when it has blocks
	// (HasBlocks).
	Blocked   int64
	HasBlocks bool
	Blocks    mailBlockList      // its blocks
	Refused   string             // HuntMail refused the block asked for: its reason
	Form      huntmail.NewBlock  // the one refused, to try again
	Prefixes  []store.MailPrefix // programs' prefixes, offered for prefix blocks
	Bundle    string
	// Senders are its top senders over SenderDays, when it lists them
	// (HasSenders).
	Senders    []huntmail.Sender
	HasSenders bool
	SenderDays int
}

// mailDomainRow is a domain's mail over the window.
type mailDomainRow struct {
	Domain   string
	Received int64
	Today    int64
	Share    float64 // of the busiest domain's, 0–100, for its bar
	SetUp    bool    // HuntMail was set up for it
}

func (s *server) mailStats(w http.ResponseWriter, r *http.Request) {
	s.renderMailStats(w, r, http.StatusOK, "", huntmail.NewBlock{})
}

// renderMailStats renders the HuntMail page; refused is why HuntMail
// didn't add form, "" when nothing was asked.
func (s *server) renderMailStats(w http.ResponseWriter, r *http.Request, status int, refused string, form huntmail.NewBlock) {
	v := &mailStatsView{Configured: s.Mail != nil, Refused: refused, Form: form}
	page := pageData{Title: "HuntMail", Nav: "emailstats", Data: v}
	if s.Mail == nil {
		s.render(w, r, status, "mailstats", page)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	st, err := s.Mail.Stats(ctx, mailStatsDays)
	if err != nil {
		v.Problem = mailProblem(err)
		s.render(w, r, status, "mailstats", page)
		return
	}
	v.S = st
	if st.MaxStoreBytes > 0 {
		v.StoredPct = min(100, float64(st.StoredBytes)*100/float64(st.MaxStoreBytes))
	}
	v.Backlog = st.ParsePending + st.ParseDeferred
	if st.Newsletters != nil {
		v.HasNL, v.Newsletters = true, *st.Newsletters
	}
	if st.Blocked != nil {
		v.HasBlocks, v.Blocked = true, *st.Blocked
		v.Blocks.Added, _ = strconv.ParseInt(r.URL.Query().Get("added"), 10, 64)
		if v.Prefixes, err = s.Store.MailPrefixes(ctx); err != nil {
			s.serverError(w, r, err)
			return
		}
		if v.Blocks.Rows, err = s.mailBlockRows(ctx, v.Prefixes); err != nil {
			s.Log.Warn("huntmail page: blocks", "err", err)
		}
	}
	switch ss, err := s.Mail.Senders(ctx, mailSenderDays, mailSenderRows); {
	case err == nil:
		v.HasSenders, v.Senders, v.SenderDays = true, ss, mailSenderDays
	case !errors.Is(err, huntmail.ErrNotFound): // not found: a HuntMail without them
		s.Log.Warn("huntmail page: top senders", "err", err)
	}
	if s.MailBundle != nil && st.Version != "" && versionLess(st.Version, s.MailBundle.Version()) {
		v.CanUpdate, v.Bundle = true, s.MailBundle.Version()
	}
	v.Domains = mailDomainRows(st)
	if len(st.Days) > 0 {
		v.HasDays, v.Chart = true, mailDayChart(st.Days)
	}
	s.render(w, r, status, "mailstats", page)
}

// mailDomainRows lists the domains busiest first, then the ones set up
// that got nothing.
func mailDomainRows(st huntmail.Stats) []mailDomainRow {
	var out []mailDomainRow
	peak := int64(0)
	for _, d := range st.Domains {
		peak = max(peak, d.Received)
	}
	for _, d := range st.Domains {
		row := mailDomainRow{Domain: d.Domain, Received: d.Received, Today: d.Today, SetUp: slices.Contains(st.DomainsSetUp, d.Domain)}
		if peak > 0 {
			row.Share = float64(d.Received) * 100 / float64(peak)
		}
		out = append(out, row)
	}
	for _, d := range st.DomainsSetUp {
		if !slices.ContainsFunc(out, func(r mailDomainRow) bool { return r.Domain == d }) {
			out = append(out, mailDomainRow{Domain: d, SetUp: true})
		}
	}
	return out
}

// mailDayChart is mail per day as columns, with a date under every few.
func mailDayChart(days []huntmail.Day) barChart {
	at := make([]time.Time, len(days))
	totals := make([]float64, len(days))
	tips := make([]string, len(days))
	for i, d := range days {
		at[i], _ = time.Parse(time.DateOnly, d.Day)
		totals[i] = float64(d.Received)
		tips[i] = at[i].Format("Mon Jan 2") + ": " + strconv.FormatInt(d.Received, 10) + " " + plural(int(d.Received), "email", "emails") + ", " + fileSize(d.RawBytes)
	}
	c := buildBars(at, func(i int) []barPart { return []barPart{{Class: "mail", H: totals[i]}} }, totals, tips,
		"Emails per day, the last "+strconv.Itoa(len(days))+" days (UTC)", func(f float64) string { return formatCount(f, "") })
	// buildBars labels months; a day chart labels a day every week or so.
	c.Ticks = c.Ticks[:0]
	slot := (100 - barGutter) / float64(len(days))
	every := max(1, (len(days)+5)/6)
	for i := len(days) - 1; i >= 0; i -= every { // from today back, so today is labelled
		c.Ticks = append(c.Ticks, barTick{At: pct2(float64(i)*slot + slot/2), Label: at[i].Format("Jan 2")})
	}
	return c
}

// versionLess compares dotted versions ("1.0.0" < "1.1.0"), numerically
// part by part; anything that isn't a number compares as text.
func versionLess(a, b string) bool {
	pa, pb := splitVersion(a), splitVersion(b)
	for i := range max(len(pa), len(pb)) {
		var x, y string
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		nx, ex := strconv.Atoi(x)
		ny, ey := strconv.Atoi(y)
		switch {
		case ex == nil && ey == nil && nx != ny:
			return nx < ny
		case (ex != nil || ey != nil) && x != y:
			return x < y
		}
	}
	return false
}

func splitVersion(v string) []string {
	var out []string
	cur := ""
	for _, r := range v {
		if r == '.' || r == '-' || r == '+' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}
