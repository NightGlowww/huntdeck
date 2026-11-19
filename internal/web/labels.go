package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mdpsec/huntdeck/internal/huntscore"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// labelTarget loads the program a label form posts to.
func (s *server) labelTarget(w http.ResponseWriter, r *http.Request) (*store.ProgramDetail, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	p, err := s.Store.Program(r.Context(), canonicalPlatform(r.PathValue("platform")), r.PathValue("handle"))
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "No such program.")
		return nil, false
	}
	if err != nil {
		s.serverError(w, r, err)
		return nil, false
	}
	return p, true
}

// programPath is a program's page; escaping keeps it on this site (a "/" in
// a handle becomes %2F, so the path can't start "//").
func programPath(p *store.ProgramDetail) string { return programRefPath(p.ProgramRef) }

// programRefPath is programPath for a program's reference.
func programRefPath(p store.ProgramRef) string {
	return "/programs/" + url.PathEscape(p.Platform) + "/" + url.PathEscape(p.Handle)
}

// setIgnored ignores a program, or stops ignoring it, and rescores so it
// leaves (or rejoins) the picks.
func (s *server) setIgnored(w http.ResponseWriter, r *http.Request) {
	p, ok := s.labelTarget(w, r)
	if !ok {
		return
	}
	ignored := isTrue(r.PostFormValue("ignored"))
	err := s.Store.SetIgnored(r.Context(), p.ID, ignored)
	if err == nil {
		err = huntscore.Refresh(r.Context(), s.Store, s.Store.Now())
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	msg := "program-unignored"
	if ignored {
		msg = "program-ignored"
	}
	http.Redirect(w, r, programPath(p)+"?msg="+msg, http.StatusSeeOther) //nolint:gosec // a local path: both parts are path-escaped
}

// setTags replaces a program's tags.
func (s *server) setTags(w http.ResponseWriter, r *http.Request) {
	p, ok := s.labelTarget(w, r)
	if !ok {
		return
	}
	tags, err := store.NormalizeTags(r.PostFormValue("tags"))
	if err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Tags: "+err.Error()+".")
		return
	}
	if err := s.Store.SetTags(r.Context(), p.ID, tags); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, programPath(p)+"?msg=program-saved", http.StatusSeeOther) //nolint:gosec // a local path: both parts are path-escaped
}

// tagParam reads a single tag filter; anything that isn't a valid tag is
// no filter.
func tagParam(v string) string {
	tags, err := store.NormalizeTags(v)
	if err != nil || len(tags) != 1 {
		return ""
	}
	return tags[0]
}

// apiScopeExport serves a program's scope for Burp Suite or ZAP.
func (s *server) apiScopeExport(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.Program(r.Context(), canonicalPlatform(r.PathValue("platform")), r.PathValue("handle"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "no such program")
		return
	}
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	if strings.HasSuffix(r.URL.Path, ".caido.json") {
		allow, deny, skipped := caidoExport(p.Entries)
		body, err := scope.CaidoConfig("HuntDeck "+p.Name, p.Name+" ("+platformLabel(p.Platform)+")", allow, deny)
		if err != nil {
			s.apiServerError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-HuntDeck-Skipped", strconv.Itoa(len(skipped)))
		w.Header().Set("Content-Disposition", `attachment; filename="`+caidoFileName(p.ProgramRef)+`"`)
		exportStamp(w, r)    // JSON: the header only
		_, _ = w.Write(body) //nolint:gosec // JSON as an attachment, never HTML (and nosniff is set)
		return
	}
	var in, out []scope.Target
	for _, e := range p.Entries {
		if e.RemovedAt != nil {
			continue
		}
		if e.InScope {
			in = append(in, e.Targets...)
		} else {
			out = append(out, e.Targets...)
		}
	}
	var body []byte
	var ctype, ext string
	zap := strings.HasSuffix(r.URL.Path, ".zap.context")
	if zap {
		body, err = scope.ZAPContext(p.Name, in, out)
		ctype, ext = "application/xml; charset=utf-8", ".context"
	} else {
		body, err = scope.BurpScope(in, out)
		ctype, ext = "application/json; charset=utf-8", ".burp.json"
	}
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	if zap {
		body = xmlComment(w, r, body)
	} else {
		exportStamp(w, r) // JSON: the header only
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="`+fileSafe(p.Platform+"-"+p.Handle)+ext+`"`)
	_, _ = w.Write(body) //nolint:gosec // JSON or XML as an attachment, never HTML (and nosniff is set)
}

// fileSafe keeps letters, digits, '-', '_' and '.' for a download name.
func fileSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, s)
}
