package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/jev"
	"github.com/mdpsec/huntdeck/internal/policycheck"
)

// programPolicyCheck asks the decision model whether a plan fits this program's
// policy. The policy is the text the Policy tab already shows (policyText); the
// plan is what the hunter typed. The verdict comes back on the same tab.
func (s *server) programPolicyCheck(w http.ResponseWriter, r *http.Request) {
	platform := canonicalPlatform(r.PathValue("platform"))
	handle := r.PathValue("handle")
	back := "/programs/" + platform + "/" + handle + "?tab=policy"
	if s.JevKey.Reveal() == "" {
		http.Redirect(w, r, back+"&policy_error=nomodel", http.StatusSeeOther)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	plan := strings.TrimSpace(r.PostFormValue("plan"))
	if plan == "" {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	p, err := s.Store.Program(r.Context(), platform, handle)
	if err != nil {
		http.Redirect(w, r, back+"&policy_error=noprogram", http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	client := &jev.Client{BaseURL: s.JevURL, Key: s.JevKey.Reveal(), Model: s.JevModel, Proxy: s.JevProxy}
	a, err := policycheck.Check(ctx, client, platform+"/"+handle, policyText(p), plan)
	if err != nil {
		s.Log.WarnContext(r.Context(), "policy check failed", "program", handle, "error", err)
		http.Redirect(w, r, back+"&policy_error=failed", http.StatusSeeOther)
		return
	}
	// The answer rides back on the query: a stored verdict would go stale the
	// moment the plan changes, and this is one call either way.
	worst := a.Rules[0]
	for _, r := range a.Rules {
		if r.Break > worst.Break {
			worst = r
		}
	}
	q := "&policy_call=" + a.Call +
		"&policy_p=" + strconv.FormatFloat(worst.Break, 'f', 2, 64) +
		"&policy_rule=" + worst.Key +
		"&policy_quote=" + urlQuery(worst.Quote) +
		"&policy_why=" + urlQuery(a.Why)
	http.Redirect(w, r, back+q, http.StatusSeeOther)
}
