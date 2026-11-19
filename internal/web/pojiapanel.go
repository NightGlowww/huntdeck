package web

import (
	"context"
	"net/http"
	"time"

	"github.com/mdpsec/huntdeck/internal/pojia"
	"github.com/mdpsec/huntdeck/internal/store"
)

// The panel's contract: a cached answer shows at once, a stale one shows at
// once and refreshes in the background, and only the very first visit to a
// program waits for POJIA — ten seconds at most. A failed fetch never writes
// "not indexed" (that is POJIA's own wording alone) and never breaks the page.
const (
	pojiaFresh    = 7 * 24 * time.Hour
	pojiaNotFound = 24 * time.Hour // a vendor may be indexed any day
	pojiaFetch    = 10 * time.Second
)

func (s *server) pojiaPanel(ctx context.Context, r *http.Request, p *store.ProgramDetail, d *programData) {
	d.PojiaEnabled = true
	cached, err := s.Store.PojiaProfileFor(ctx, p.ID)
	if err == nil {
		ttl := pojiaFresh
		if !cached.Found {
			ttl = pojiaNotFound
		}
		if time.Since(cached.FetchedAt) < ttl {
			d.Pojia = cached
			return
		}
		// Stale: show it now, refresh for the next visit.
		d.Pojia = cached
		d.PojiaStale = true
		s.pojiaRefresh(r.Context(), p, nil)
		return
	}
	// No cache at all: this program's first visit pays one inline fetch.
	s.pojiaFetchInline(r.Context(), p, d)
}

// pojiaFetchInline waits for the answer (first visit only).
func (s *server) pojiaFetchInline(ctx context.Context, p *store.ProgramDetail, d *programData) {
	ctx, cancel := context.WithTimeout(ctx, pojiaFetch)
	defer cancel()
	client := &pojia.Client{BaseURL: s.PojiaURL, Key: s.PojiaKey.Reveal()}
	profile, found, err := client.ProgramProfile(ctx, p.Handle)
	if err != nil {
		s.Log.WarnContext(ctx, "program: cannot read the POJIA profile", "program", p.Handle, "error", err)
		d.PojiaFailed = true
		return
	}
	saved := store.PojiaProfile{ProgramID: p.ID, Handle: p.Handle, Profile: profile,
		Found: found, FetchedAt: time.Now()}
	if err := s.Store.SavePojiaProfile(ctx, saved); err != nil {
		s.Log.WarnContext(ctx, "program: cannot cache the POJIA profile", "error", err)
	}
	d.Pojia = &saved
}

// pojiaRefresh updates the cache without holding a page open.
func (s *server) pojiaRefresh(ctx context.Context, p *store.ProgramDetail, done chan<- struct{}) {
	client := &pojia.Client{BaseURL: s.PojiaURL, Key: s.PojiaKey.Reveal()}
	// The request is over by design: this refresh outlives it and must not be
	// cancelled with it, so a fresh context is the point, not an oversight.
	go func() { //nolint:gosec // G118: the goroutine must outlive the request
		if done != nil {
			defer close(done)
		}
		ctx, cancel := context.WithTimeout(context.Background(), pojiaFetch)
		defer cancel()
		profile, found, err := client.ProgramProfile(ctx, p.Handle)
		if err != nil {
			s.Log.WarnContext(ctx, "pojia: background refresh failed", "program", p.Handle, "error", err)
			return
		}
		saved := store.PojiaProfile{ProgramID: p.ID, Handle: p.Handle, Profile: profile,
			Found: found, FetchedAt: time.Now()}
		if err := s.Store.SavePojiaProfile(ctx, saved); err != nil {
			s.Log.WarnContext(ctx, "pojia: cannot cache the refresh", "error", err)
		}
	}()
}
