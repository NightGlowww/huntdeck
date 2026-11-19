package web

import (
	"net/http"

	"github.com/mdpsec/huntdeck/internal/mcpserver"
	"github.com/mdpsec/huntdeck/internal/version"
)

// mcpRPC is the streamable-HTTP MCP endpoint: the transport a client in
// another process uses, where stdio cannot. The bearer key that opened the
// gate is the same one the tool calls carry to the API, so one connection,
// one key, no session to keep.
func (s *server) mcpRPC(w http.ResponseWriter, r *http.Request) {
	srv := mcpserver.New(baseURL(r), "", version.Version, false)
	srv.Token = r.Header.Get("Authorization")
	if i := indexOf(srv.Token, " "); i > 0 {
		srv.Token = srv.Token[i+1:]
	}
	srv.HTTPHandler().ServeHTTP(w, r)
}

func indexOf(s, sep string) int {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}
