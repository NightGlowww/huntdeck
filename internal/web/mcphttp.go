package web

import (
	"net/http"
	"strings"
	"sync"

	"github.com/mdpsec/huntdeck/internal/mcpserver"
	"github.com/mdpsec/huntdeck/internal/version"
)

// mcpOnce builds one Server per process for the HTTP transport: the call
// budget and the poll cooldown live on the struct, so a per-request build
// would reset them every POST and an agent that loops would never hit the
// ceiling the comments promise.
var (
	mcpOnce  sync.Once
	mcpSrv   *mcpserver.Server
	mcpBase  string
	mcpReady bool
)

func (s *server) sharedMCP(base, authHeader string) *mcpserver.Server {
	mcpOnce.Do(func() {
		mcpSrv = mcpserver.New(base, "", version.Version, false)
		mcpBase = base
		mcpReady = true
	})
	// The bearer key that opened the gate rides along for the loopback calls;
	// a session cookie cannot follow into the loopback client, so a cookie-only
	// request is told to send the header instead of failing on every tool call.
	mcpSrv.Token = strings.TrimPrefix(authHeader, "Bearer ")
	return mcpSrv
}

// mcpRPC is the streamable-HTTP MCP endpoint: the transport a client in
// another process uses, where stdio cannot. The bearer key that opened the
// gate is the same one the tool calls carry to the API, so one connection,
// one key, no session to keep.
func (s *server) mcpRPC(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") == "" && r.Cookies() != nil {
		for _, c := range r.Cookies() {
			if c.Name == "hd_session" {
				// The gate passed on the cookie, but the loopback tool calls carry
				// no credential and would fail one by one. Say it once, plainly.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32001,"message":"send Authorization: Bearer <mcp key> (mint one on /mcp), not a session cookie"}}`))
				return
			}
		}
	}
	s.sharedMCP(baseURL(r), r.Header.Get("Authorization")).HTTPHandler().ServeHTTP(w, r)
}
