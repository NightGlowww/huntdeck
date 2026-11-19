package web

import (
	"net/http"
	"time"

	"github.com/mdpsec/huntdeck/internal/policyai"
)

// extractPolicies reads a few policies with the configured model, from the
// settings page. It is deliberately small and bounded: three programs, twenty
// seconds each, then back to the page. The command line is there for a larger
// run.
func (s *server) extractPolicies(w http.ResponseWriter, r *http.Request) {
	if s.AI == nil || !s.AI.Configured() {
		http.Redirect(w, r, "/settings?msg=ai-off#optional", http.StatusSeeOther)
		return
	}
	res, err := policyai.Extract(r.Context(), s.Store, s.AI, policyai.Options{Limit: 3, PerItem: 20 * time.Second})
	switch {
	case err != nil || (res.Failed > 0 && res.Read == 0):
		http.Redirect(w, r, "/settings?msg=ai-failed#optional", http.StatusSeeOther)
	case res.Read > 0:
		http.Redirect(w, r, "/settings?msg=ai-read#optional", http.StatusSeeOther)
	default:
		http.Redirect(w, r, "/settings?msg=ai-none#optional", http.StatusSeeOther)
	}
}
