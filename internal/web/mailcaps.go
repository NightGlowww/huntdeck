package web

import (
	"context"
	"slices"
	"sync"
	"time"
)

// What HuntDeck keeps of HuntMail's stats between pages: its domains, how
// much mail it holds, and what it can do (keep a list to a domain, tell
// newsletters apart). Read when a page needs them, never in the background.

// mailCount is how much mail HuntMail holds, and how much of it is
// newsletters (0 from versions that don't tell them apart).
type mailCount struct {
	All, Newsletters int64
}

// mailTotals is how much mail HuntMail holds, read at most every minute.
func (s *server) mailTotals(ctx context.Context) (mailCount, error) {
	c := &s.mailDomainList
	c.mu.Lock()
	if time.Since(c.totalAt) < time.Minute {
		defer c.mu.Unlock()
		return c.total, nil
	}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	st, err := s.Mail.Stats(ctx, 0)
	if err != nil {
		return mailCount{}, err
	}
	n := mailCount{All: st.Messages}
	if st.Newsletters != nil {
		n.Newsletters = *st.Newsletters
	}
	c.mu.Lock()
	c.total, c.totalAt = n, time.Now()
	c.newsletters, c.blocks = st.Newsletters != nil, st.Blocked != nil
	c.mu.Unlock()
	return n, nil
}

// mailDomains are the domains new addresses can be on, the first one
// offered first: HUNTMAIL_DOMAINS, else the ones HuntMail was set up for
// (the busiest first), else the ones it got mail on.
// HuntMail's list is kept for a few minutes.
type mailDomainCache struct {
	mu      sync.Mutex
	at      time.Time
	list    []string
	total   mailCount // how much mail HuntMail holds (mailTotals)
	totalAt time.Time
	// byDomain says HuntMail can keep a list to a domain (to_domain): the
	// versions that can also send daily counts.
	byDomain bool
	// newsletters says HuntMail tells newsletters apart (its stats count
	// them): its lists can leave them out.
	newsletters bool
	// blocks says HuntMail has blocks (its stats count what they turned
	// away).
	blocks bool
}

// mailCanBlock says HuntMail has blocks, reading its stats if no page has
// yet (mailDomains, cached).
func (s *server) mailCanBlock(ctx context.Context) bool {
	if s.Mail == nil {
		return false
	}
	if _, err := s.mailDomains(ctx); err != nil {
		return false
	}
	s.mailDomainList.mu.Lock()
	defer s.mailDomainList.mu.Unlock()
	return s.mailDomainList.blocks
}

// mailTellsNewsletters says HuntMail tells newsletters apart, as its last
// stats said (mailDomains reads them).
func (s *server) mailTellsNewsletters() bool {
	s.mailDomainList.mu.Lock()
	defer s.mailDomainList.mu.Unlock()
	return s.mailDomainList.newsletters
}

// mailByDomain says HuntMail can keep a list to a domain, as its last
// stats said (mailDomains reads them).
func (s *server) mailByDomain() bool {
	s.mailDomainList.mu.Lock()
	defer s.mailDomainList.mu.Unlock()
	return s.mailDomainList.byDomain
}

// mailDomains are the domains to make addresses on and filter by:
// HUNTMAIL_DOMAINS, else HuntMail's. HuntMail's stats are read either way
// (at most every 5 minutes), for mailByDomain, mailTellsNewsletters and
// mailCanBlock.
func (s *server) mailDomains(ctx context.Context) ([]string, error) {
	if s.Mail == nil {
		return s.MailDomains, nil
	}
	c := &s.mailDomainList
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) < 5*time.Minute && len(c.list) > 0 {
		return s.domainsOr(c.list), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	st, err := s.Mail.Stats(ctx, 1)
	if err != nil {
		return nil, err
	}
	c.byDomain = len(st.Days) > 0
	c.newsletters, c.blocks = st.Newsletters != nil, st.Blocked != nil
	// The ones it was set up for, the busiest first (st.Domains comes most
	// mail first); else the ones it got mail on.
	var list []string
	for _, d := range st.Domains {
		if len(st.DomainsSetUp) == 0 || slices.Contains(st.DomainsSetUp, d.Domain) {
			list = append(list, d.Domain)
		}
	}
	for _, d := range st.DomainsSetUp {
		if !slices.Contains(list, d) {
			list = append(list, d)
		}
	}
	c.list, c.at = list, time.Now()
	return s.domainsOr(list), nil
}

// domainsOr is HUNTMAIL_DOMAINS when set, else HuntMail's.
func (s *server) domainsOr(huntmails []string) []string {
	if len(s.MailDomains) > 0 {
		return s.MailDomains
	}
	return huntmails
}
