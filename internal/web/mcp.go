package web

import (
	"net/http"
	"strconv"

	"github.com/mdpsec/huntdeck/internal/mcpserver"
	"github.com/mdpsec/huntdeck/internal/store"
)

// The MCP page manages its own keys: minted here, listed here, revoked here.
// They open the same gate as the password — that part does not change — but
// they are a kind of their own, so the API page's token list is not where an
// agent's key goes to be managed.
func (s *server) mcpPage(w http.ResponseWriter, r *http.Request) {
	d := s.mcpPageData(r)
	s.render(w, r, http.StatusOK, "mcp", pageData{Title: "MCP server", Nav: "mcp", Data: d})
}

func (s *server) mcpPageData(r *http.Request) mcpPageData {
	d := mcpPageData{Base: baseURL(r), Tools: mcpserver.Catalog()}
	if all, err := s.Store.APITokens(r.Context()); err == nil {
		for _, t := range all {
			if t.Kind == "mcp" {
				d.Keys = append(d.Keys, t)
			}
		}
	}
	return d
}

// mcpToken mints a key of the MCP kind, with an mcp_ prefix on the value
// itself, so what a client holds is identifiable as an MCP key and nothing
// else. Shown once, in the config, like every key.
func (s *server) mcpToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	value, err := newTokenValue()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	value = "mcp_" + value
	if _, err := s.Store.CreateAPIToken(r.Context(), "mcp", "MCP client", value); err != nil {
		s.serverError(w, r, err)
		return
	}
	d := s.mcpPageData(r)
	d.NewToken = value
	s.render(w, r, http.StatusOK, "mcp", pageData{Title: "MCP server", Nav: "mcp", Data: d})
}

// mcpRevoke retires one of this page's keys.
func (s *server) mcpRevoke(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	id, err := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	if err != nil {
		id = 0
	}
	if err := s.Store.RevokeAPITokenByKind(r.Context(), "mcp", id); err == nil {
		s.render(w, r, http.StatusOK, "mcp", pageData{Title: "MCP server", Nav: "mcp",
			Data: func() mcpPageData { d := s.mcpPageData(r); d.Flash = "revoked"; return d }()})
		return
	}
	d := s.mcpPageData(r)
	d.Flash = "missing"
	s.render(w, r, http.StatusOK, "mcp", pageData{Title: "MCP server", Nav: "mcp", Data: d})
}

type mcpPageData struct {
	Base  string
	Tools []mcpserver.ToolInfo
	// Keys are this page's own, live and revoked.
	Keys []store.APIToken
	// NewToken is a key just minted, shown once in the config.
	NewToken string
	// Flash is "" or "revoked" or "missing".
	Flash string
}
