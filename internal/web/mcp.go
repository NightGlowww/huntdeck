package web

import (
	"net/http"

	"github.com/mdpsec/huntdeck/internal/mcpserver"
)

// mcpPage shows the tool server: what it is, how to run it, and the tools it
// offers. The list comes from mcpserver.Catalog, the registry the server answers
// tools/list from, so the page cannot drift from what an agent sees.
func (s *server) mcpPage(w http.ResponseWriter, r *http.Request) {
	d := mcpPageData{Base: baseURL(r), Tools: mcpserver.Catalog()}
	s.render(w, r, http.StatusOK, "mcp", pageData{Title: "MCP server", Nav: "mcp", Data: d})
}

type mcpPageData struct {
	Base  string
	Tools []mcpserver.ToolInfo
	// NewToken is a key just minted for this connection, shown once in the
	// config blocks.
	NewToken string
}
