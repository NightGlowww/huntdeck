package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/mdpsec/huntdeck/internal/huntscore"
	"github.com/mdpsec/huntdeck/internal/store"
)

// ignoreMany ignores (or unignores) several programs at once: the Programs
// list's selection bar sends p=platform/handle for each.
func (s *server) ignoreMany(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "That form didn't read.")
		return
	}
	refs := r.PostForm["p"]
	if len(refs) == 0 || len(refs) > 500 {
		s.renderError(w, r, http.StatusBadRequest, "Pick between 1 and 500 programs.")
		return
	}
	ignored := isTrue(r.PostFormValue("ignored"))
	for _, ref := range refs {
		platform, handle, ok := strings.Cut(ref, "/")
		if !ok || platform == "" || handle == "" {
			continue
		}
		p, err := s.Store.Program(r.Context(), canonicalPlatform(platform), handle)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if err := s.Store.SetIgnored(r.Context(), p.ID, ignored); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	if err := huntscore.Refresh(r.Context(), s.Store, s.Store.Now()); err != nil {
		s.serverError(w, r, err)
		return
	}
	back := localPath(r.PostFormValue("back"), "/programs")
	msg := "program-unignored"
	if ignored {
		msg = "program-ignored"
	}
	sep := "?"
	if strings.Contains(back, "?") {
		sep = "&"
	}
	http.Redirect(w, r, back+sep+"msg="+msg, http.StatusSeeOther) //nolint:gosec // a local path
}
