package web

import (
	"context"
	"net/http"
	"time"

	"github.com/mdpsec/huntdeck/internal/pojia"
	"github.com/mdpsec/huntdeck/internal/store"
)

const pojiaCacheTTL = 7 * 24 * time.Hour

// pojiaPanel fills the program page's POJIA block: the disclosed-report
// history of this program, from POJIA's index. The answer is cached for a
// week; a fetch that fails shows the cache if there is one, and the panel
// never breaks the page.
func (s *server) pojiaPanel(ctx context.Context, r *http.Request, p *store.ProgramDetail, d *programData) {
	d.PojiaEnabled = true
	if cached, err := s.Store.PojiaProfileFor(ctx, p.ID); err == nil && time.Since(cached.FetchedAt) < pojiaCacheTTL {
		d.Pojia = cached
		return
	}
	client := &pojia.Client{BaseURL: s.PojiaURL, Key: s.PojiaKey.Reveal()}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	profile, found, err := client.ProgramProfile(ctx, p.Handle)
	if err != nil {
		s.Log.WarnContext(r.Context(), "program: cannot read the POJIA profile", "program", p.Handle, "error", err)
		// The cache, stale as it is, still beats an empty panel.
		if cached, cerr := s.Store.PojiaProfileFor(r.Context(), p.ID); cerr == nil {
			d.Pojia = cached
			d.PojiaStale = true
		}
		return
	}
	saved := store.PojiaProfile{ProgramID: p.ID, Handle: p.Handle, Profile: profile, Found: found}
	if err := s.Store.SavePojiaProfile(r.Context(), saved); err != nil {
		s.Log.WarnContext(r.Context(), "program: cannot cache the POJIA profile", "error", err)
	}
	d.Pojia = &saved
}
