package web

import (
	"net/http"

	"github.com/mdpsec/huntdeck/internal/mcpserver"
)

// mcpToken mints a token named "mcp" for one client connection, so the key an
// agent holds is its own: minted here, revoked on the API page, and never
// whatever else uses the API. The new value fills the page's config blocks.
func (s *server) mcpToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	value, err := newTokenValue()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if _, err := s.Store.CreateAPIToken(r.Context(), "mcp", value); err != nil {
		s.serverError(w, r, err)
		return
	}
	d := mcpPageData{Base: baseURL(r), Tools: mcpserver.Catalog(), NewToken: value}
	s.render(w, r, http.StatusOK, "mcp", pageData{Title: "MCP server", Nav: "mcp", Data: d})
}
