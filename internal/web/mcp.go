package web

import (
	"net/http"
	"strconv"
	"time"

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

// mcpKeysShown is how many keys the page lists by default. The list grows one
// row per connection a client makes; a hundred identical rows is not a list
// anybody reads, and the ones worth revoking are almost always recent.
const mcpKeysShown = 20

func (s *server) mcpPageData(r *http.Request) mcpPageData {
	d := mcpPageData{Base: baseURL(r), Tools: mcpserver.Catalog()}
	showAll := r.URL.Query().Get("all") != ""
	if all, err := s.Store.APITokens(r.Context()); err == nil {
		for _, t := range all {
			if t.Kind != "mcp" {
				continue
			}
			d.Keys = append(d.Keys, t)
		}
	}
	// APITokens is newest first, so the cut keeps the newest and counts the rest.
	if !showAll && len(d.Keys) > mcpKeysShown {
		d.Hidden = len(d.Keys) - mcpKeysShown
		d.Keys = d.Keys[:mcpKeysShown]
	}
	d.ShowAll = showAll
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

// mcpDelete removes keys from the list. Revoking keeps the row forever, which is
// right for one key someone wants to remember and wrong for the twenty an
// afternoon of reconnecting leaves behind: this deletes, one or many.
func (s *server) mcpDelete(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		s.render(w, r, http.StatusBadRequest, "mcp", pageData{Title: "MCP server", Nav: "mcp",
			Data: s.mcpPageData(r)})
		return
	}
	ids := make([]int64, 0, len(r.PostForm["id"]))
	for _, raw := range r.PostForm["id"] {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	n, err := s.Store.DeleteAPITokens(r.Context(), "mcp", ids)
	d := s.mcpPageData(r)
	switch {
	case err != nil:
		s.Log.WarnContext(r.Context(), "mcp: delete keys", "error", err)
		d.Flash = "deletefailed"
	case n == 0:
		d.Flash = "missing"
	default:
		d.Flash = "deleted"
		d.Deleted = n
	}
	s.render(w, r, http.StatusOK, "mcp", pageData{Title: "MCP server", Nav: "mcp", Data: d})
}

type mcpPageData struct {
	Base  string
	Tools []mcpserver.ToolInfo
	// Keys are this page's own, live and revoked.
	Keys []store.APIToken
	// NewToken is a key just minted, shown once in the config.
	NewToken string
	// Hidden is how many older keys the page does not show.
	Hidden int
	// ShowAll says the page was asked for every key.
	ShowAll bool
	// Deleted is how many keys the last delete removed.
	Deleted int
	// Flash is "", "revoked", "deleted", "deletefailed" or "missing".
	Flash string
}

// mcpDeleteUnused removes the keys nobody ever used. A list of 141 identical
// rows is what this is for: a test that minted one is indistinguishable from a
// client that lost its token, and ticking them off one at a time is not a thing
// anybody does.
func (s *server) mcpDeleteUnused(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		s.render(w, r, http.StatusBadRequest, "mcp", pageData{Title: "MCP server", Nav: "mcp",
			Data: s.mcpPageData(r)})
		return
	}
	// A key made minutes ago may still be in a paste buffer: only older ones go.
	const unusedGrace = time.Hour
	n, err := s.Store.DeleteUnusedAPITokens(r.Context(), "mcp", time.Now().Add(-unusedGrace))
	d := s.mcpPageData(r)
	switch {
	case err != nil:
		s.Log.WarnContext(r.Context(), "mcp: delete unused keys", "error", err)
		d.Flash = "deletefailed"
	case n == 0:
		d.Flash = "nounused"
	default:
		d.Flash = "deleted"
		d.Deleted = n
	}
	s.render(w, r, http.StatusOK, "mcp", pageData{Title: "MCP server", Nav: "mcp", Data: d})
}
