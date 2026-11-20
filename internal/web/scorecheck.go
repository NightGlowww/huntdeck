package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/jev"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scorecheck"
	"github.com/mdpsec/huntdeck/internal/store"
)

// methodologyCheck audits the figures a program's page shows against the data
// HuntDeck stores. It is the one place a decision model helps the scoring
// method: the score itself is arithmetic, and the model has no better data.
func (s *server) methodologyCheck(w http.ResponseWriter, r *http.Request) {
	d := pageData{Title: "Methodology", Nav: "methodology"}
	d.ScoreCheckOn = s.JevKey.Reveal() != ""
	if s.JevKey.Reveal() == "" {
		d.ScoreCheckError = "nomodel"
		s.render(w, r, http.StatusOK, "methodology", d)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	platform := canonicalPlatform(strings.TrimSpace(r.PostFormValue("platform")))
	handle := strings.TrimSpace(r.PostFormValue("handle"))
	d.ScoreCheckProgram = platform + "/" + handle
	if platform == "" || handle == "" {
		d.ScoreCheckError = "noprogram"
		s.render(w, r, http.StatusOK, "methodology", d)
		return
	}
	p, err := s.Store.Program(r.Context(), platform, handle)
	if err != nil {
		d.ScoreCheckError = "notfound"
		s.render(w, r, http.StatusOK, "methodology", d)
		return
	}
	facts := scoreFacts(p)
	if len(facts.Claims) == 0 {
		d.ScoreCheckError = "nofigures"
		s.render(w, r, http.StatusOK, "methodology", d)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	client := &jev.Client{BaseURL: s.JevURL, Key: s.JevKey.Reveal(), Model: s.JevModel, Proxy: s.JevProxy}
	verdicts, err := scorecheck.Check(ctx, client, facts)
	if err != nil {
		s.Log.WarnContext(r.Context(), "score check failed", "program", d.ScoreCheckProgram, "error", err)
		d.ScoreCheckError = "failed"
		s.render(w, r, http.StatusOK, "methodology", d)
		return
	}
	d.ScoreCheckRows = verdicts
	s.render(w, r, http.StatusOK, "methodology", d)
}

// scoreFacts turns a program into the claims its page makes and the stored
// values behind them. Both sides use the page's own wording, so the audit is
// about what a reader sees, not about a number in a struct.
func scoreFacts(p *store.ProgramDetail) scorecheck.Facts {
	f := scorecheck.Facts{
		Program:  p.Platform + "/" + p.Handle,
		Evidence: map[string]string{},
		Claims:   map[string]string{},
	}
	add := func(field, evidence, claim string) {
		if strings.TrimSpace(evidence) == "" {
			return
		}
		f.Evidence[field] = evidence
		f.Claims[field] = claim
	}
	if p.State != "" {
		add("state", p.State, "the program's state is "+p.State)
	}
	if p.OffersBounty {
		add("bounty", "yes", "the program offers a bounty")
	} else {
		add("bounty", "no", "the program offers no bounty")
	}
	// The bounty range is the rewards table, not a single field: the top of the
	// highest row is what the page calls the top bounty.
	if st := p.PublicStats; st != nil && len(st.Rewards) > 0 {
		top := 0.0
		for _, row := range st.Rewards {
			for _, r := range []platform.RewardRange{row.Low, row.Medium, row.High, row.Critical} {
				if r.Max > top {
					top = r.Max
				}
			}
		}
		if top > 0 {
			add("bounty_max", strconv.FormatFloat(top, 'f', 0, 64),
				"the top bounty is "+humanize.Money(top, p.Currency))
		}
	}
	if st := p.PublicStats; st != nil {
		if v := st.BountiesPaidLast90d; v != nil && *v > 0 {
			add("paid_90d", strconv.FormatFloat(*v, 'f', 0, 64),
				"the program paid "+humanize.Money(*v, p.Currency)+" in the last 90 days")
		}
		if v := st.ReportsLast90d; v != nil && *v > 0 {
			add("reports_90d", strconv.Itoa(*v),
				"the program received "+strconv.Itoa(*v)+" reports in the last 90 days")
		}
	}
	return f
}
