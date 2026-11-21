package web

import (
	"net/http"
	"strconv"
)

// apiTokenIDs reads a repeated id field from the bulk token forms, dropping
// malformed and duplicate values before any store operation runs.
func apiTokenIDs(r *http.Request) []int64 {
	_ = r.ParseForm()
	seen := make(map[int64]struct{})
	ids := make([]int64, 0, len(r.PostForm["id"]))
	for _, raw := range r.PostForm["id"] {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

// revokeAPITokens retires the selected live API tokens in one page action.
// Already revoked rows are harmless and are left for the delete action.
func (s *server) revokeAPITokens(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	ids := apiTokenIDs(r)
	count := 0
	for _, id := range ids {
		if err := s.Store.RevokeAPITokenByKind(r.Context(), "api", id); err == nil {
			count++
		}
	}
	s.renderAPIDocsResult(w, r, "", "token-bulk-revoked", count)
}

// deleteAPITokens removes only revoked API tokens. A selected live token is
// intentionally retained, preserving the same two-step safety as single-row
// deletion and making the bulk action safe to use with Select all.
func (s *server) deleteAPITokens(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	ids := apiTokenIDs(r)
	count, err := s.Store.DeleteRevokedAPITokens(r.Context(), "api", ids)
	s.renderAPIDocsResult(w, r, "", "token-bulk-deleted", count)
	if err != nil {
		s.Log.WarnContext(r.Context(), "api: bulk delete tokens", "error", err)
	}
}
