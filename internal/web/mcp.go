package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
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
	catalog := mcpserver.Catalog()
	d := mcpPageData{
		Base:   baseURL(r),
		Tools:  catalog,
		Groups: buildMCPGroups(catalog),
	}
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
	Base   string
	Tools  []mcpserver.ToolInfo
	Groups []mcpToolGroup
	// Keys are this page's own, live and retired.
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

type mcpToolGroup struct {
	ID          string
	Name        string
	Description string
	Tools       []mcpToolView
}

type mcpToolView struct {
	mcpserver.ToolInfo
	IsWrite     bool
	Summary     string
	ExampleJSON string
}

type mcpCategorySpec struct {
	id          string
	name        string
	description string
	tools       []string
}

var mcpCategories = []mcpCategorySpec{
	{
		id:          "overview-queue",
		name:        "Overview & Queue",
		description: "Instance status, Hunt board snapshot, prioritized targets and platform stats.",
		tools:       []string{"huntdeck_status", "huntdeck_board", "huntdeck_queue", "huntdeck_stats"},
	},
	{
		id:          "programs-scope",
		name:        "Programs & Scope",
		description: "Browse programs, private invites, policy compliance and plain-text scope.",
		tools:       []string{"huntdeck_programs", "huntdeck_programs_csv", "huntdeck_program", "huntdeck_invites", "huntdeck_scope_text", "huntdeck_policy_check"},
	},
	{
		id:          "targets-matching",
		name:        "Targets & Matching",
		description: "Filter in-scope assets, match hosts in bulk and export scanner scopes.",
		tools:       []string{"huntdeck_targets", "huntdeck_match", "huntdeck_match_many", "huntdeck_export"},
	},
	{
		id:          "changes-actions",
		name:        "Changes & Actions",
		description: "Inspect scope churn, disclosed reports, trigger polls and triage findings.",
		tools:       []string{"huntdeck_changes", "huntdeck_changes_text", "huntdeck_disclosed", "huntdeck_poll", "huntdeck_triage"},
	},
}

func buildMCPGroups(catalog []mcpserver.ToolInfo) []mcpToolGroup {
	byName := make(map[string]mcpserver.ToolInfo, len(catalog))
	for _, t := range catalog {
		byName[t.Name] = t
	}

	assigned := make(map[string]bool, len(catalog))
	groups := make([]mcpToolGroup, 0, len(mcpCategories)+1)
	for _, cat := range mcpCategories {
		g := mcpToolGroup{
			ID:          cat.id,
			Name:        cat.name,
			Description: cat.description,
		}
		for _, name := range cat.tools {
			t, ok := byName[name]
			if !ok {
				continue
			}
			assigned[name] = true
			g.Tools = append(g.Tools, buildMCPToolView(t))
		}
		if len(g.Tools) > 0 {
			groups = append(groups, g)
		}
	}

	var other mcpToolGroup
	other.ID = "other"
	other.Name = "Other"
	other.Description = "Additional tools registered by the MCP server."
	for _, t := range catalog {
		if assigned[t.Name] {
			continue
		}
		other.Tools = append(other.Tools, buildMCPToolView(t))
	}
	if len(other.Tools) > 0 {
		groups = append(groups, other)
	}
	return groups
}

func buildMCPToolView(t mcpserver.ToolInfo) mcpToolView {
	return mcpToolView{
		ToolInfo:    t,
		IsWrite:     t.Write || t.Name == "huntdeck_triage",
		Summary:     summarizeMCPDescription(t.Description),
		ExampleJSON: buildMCPExampleJSON(t),
	}
}

func summarizeMCPDescription(desc string) string {
	s := strings.TrimSpace(desc)
	if s == "" {
		return ""
	}
	if idx := strings.Index(s, ". "); idx > 0 && idx < 96 {
		return s[:idx+1]
	}
	if r := []rune(s); len(r) > 96 {
		return string(r[:95]) + "…"
	}
	return s
}

type mcpRPCExample struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      int               `json:"id"`
	Method  string            `json:"method"`
	Params  mcpRPCExampleCall `json:"params"`
}

type mcpRPCExampleCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func buildMCPExampleJSON(t mcpserver.ToolInfo) string {
	args := make(map[string]any)
	for _, arg := range t.Arguments {
		if arg.Required {
			args[arg.Name] = mcpExampleValue(t.Name, arg)
		}
	}
	// When a tool takes only optional arguments, surface a couple of
	// representative filters so the JSON-RPC example stays concrete.
	if len(args) == 0 && len(t.Arguments) > 0 {
		preferred := []string{"platform", "q", "since", "kind", "limit", "paying", "bounty", "severity"}
		byName := make(map[string]mcpserver.ArgInfo, len(t.Arguments))
		for _, arg := range t.Arguments {
			byName[arg.Name] = arg
		}
		for _, name := range preferred {
			if arg, ok := byName[name]; ok {
				args[arg.Name] = mcpExampleValue(t.Name, arg)
				if len(args) >= 3 {
					break
				}
			}
		}
		if len(args) == 0 {
			for _, arg := range t.Arguments {
				args[arg.Name] = mcpExampleValue(t.Name, arg)
				if len(args) >= 2 {
					break
				}
			}
		}
	}

	payload := mcpRPCExample{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mcpRPCExampleCall{
			Name:      t.Name,
			Arguments: args,
		},
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func mcpExampleValue(toolName string, arg mcpserver.ArgInfo) any {
	switch arg.Name {
	case "platform":
		return "hackerone"
	case "handle":
		return "acme"
	case "host":
		return "api.example.com"
	case "hosts":
		return "api.example.com\nadmin.example.com"
	case "program":
		return "hackerone/acme"
	case "q":
		return "api"
	case "since":
		if toolName == "huntdeck_scope_text" {
			return "30d"
		}
		return "7d"
	case "added":
		return "7d"
	case "kind":
		if strings.HasPrefix(toolName, "huntdeck_changes") {
			return "scope_added"
		}
		return "wildcard"
	case "format":
		return "burp"
	case "parts":
		return "facts,brief,rules"
	case "state":
		return "open"
	case "bounty":
		return "1"
	case "sort":
		return "huntscore"
	case "severity":
		return "high"
	case "show":
		return "in"
	case "type":
		return "wildcard"
	case "limit":
		return 20
	case "title":
		return "IDOR on /api/v1/invoices"
	case "target":
		return "api.example.com"
	case "in_scope":
		return "*.example.com is listed in scope for bounties"
	case "impact":
		return "Authenticated user can read another tenant's invoice PDF"
	case "evidence":
		return "HTTP 200 response returning cross-tenant invoice metadata"
	case "plan":
		return "Authenticated API testing at 2 req/s without automated scanners"
	}
	switch arg.Type {
	case "integer", "number":
		return 10
	case "boolean":
		return true
	default:
		return "example"
	}
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
