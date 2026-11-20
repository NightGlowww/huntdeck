package web

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/mdpsec/huntdeck/internal/store"
)

// sortURL is a list at path with query, sorted by key on its first page:
// sorting by the current key again turns the order around, while a new key
// starts in its usual direction.
func sortURL(path string, query url.Values, f store.ProgramFilter, key string) string {
	q := url.Values{}
	for k, v := range query {
		q[k] = v
	}
	q.Set("sort", key)
	q.Del("page")
	q.Del("rev")
	if key == f.Sort && !f.Reverse {
		q.Set("rev", "1")
	}
	return path + "?" + q.Encode()
}

// sortClass is a sortable column header's class: "on" marks the current
// sort, "rev" that it's turned around.
func sortClass(on, reversed bool) string {
	switch {
	case on && reversed:
		return "sort on rev"
	case on:
		return "sort on"
	}
	return "sort"
}

// methodology explains how HuntDeck works out its numbers: where each one
// comes from on each platform, what is estimated, and HuntScore.
func (s *server) methodology(w http.ResponseWriter, r *http.Request) {
	d := pageData{Title: "Methodology", Nav: "methodology"}
	// The audit panel is offered only when a decision model is configured; the
	// POST handler sets the same flag when it renders the answer.
	d.ScoreCheckOn = s.JevKey.Reveal() != ""
	s.render(w, r, http.StatusOK, "methodology", d)
}

// search serves the top bar: hosts and URLs go to the scope check;
// anything else searches program names, and goes to the scope check (which
// finds scope mentioning it) when no program's name has it. A name search
// says how much scope mentions the words too.
func (s *server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		http.Redirect(w, r, "/programs", http.StatusSeeOther)
		return
	}
	names := url.Values{"q": {q}, "state": {"all"}}
	if !looksLikeHost(q) {
		f := programFilterFrom(names)
		f.Limit = 1
		_, n, err := s.Store.Programs(r.Context(), f)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if n > 0 {
			http.Redirect(w, r, "/programs?"+names.Encode(), http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/match?"+url.Values{"q": {q}}.Encode(), http.StatusSeeOther)
}

// looksLikeHost reports whether q reads as a hostname, IP or URL rather
// than a program name.
func looksLikeHost(q string) bool {
	if strings.ContainsAny(q, " \t") {
		return false
	}
	switch {
	case strings.Contains(q, "://"), strings.Count(q, ":") >= 2: // URL or IPv6
		return true
	default:
		return strings.Contains(strings.TrimSuffix(q, "."), ".")
	}
}

func (s *server) platformNames() []string {
	names := make([]string, 0, len(s.Platforms))
	for _, p := range s.Platforms {
		names = append(names, p.Name)
	}
	return names
}

// oneOf returns v if it is one of allowed, else allowed[0].
func oneOf(v string, allowed ...string) string {
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return allowed[0]
}

func isTrue(v string) bool {
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func canonicalPlatform(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "h1":
		return "hackerone"
	case "bc":
		return "bugcrowd"
	case "it":
		return "intigriti"
	case "ywh":
		return "yeswehack"
	}
	return n
}
