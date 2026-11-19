package web

import (
	"net/http"
	"strings"
	"testing"
)

// The page mints a key named mcp for one connection. The whole point is
// that the key is its own, so the test drives the real MCP binary with it —
// a key that only decorates the config would pass every other check.
func TestMCPPageMintsAWorkingKey(t *testing.T) {
	e := newTestEnv(t)
	res, body := e.do(e.client(), http.MethodPost, "/mcp/token", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("mint: %d", res.StatusCode)
	}
	i := strings.Index(body, "HUNTDECK_TOKEN\": \"")
	if i < 0 {
		t.Fatal("the page does not carry the minted key")
	}
	tok := body[i+len("HUNTDECK_TOKEN\": \""):]
	tok = tok[:strings.Index(tok, "\"")]
	if len(tok) != len("mcp_")+64 {
		t.Fatalf("key is %d chars, want 64", len(tok))
	}
	// The key opens the same gate as the password.
	res2, _ := e.do(e.client(), http.MethodGet, "/api/v1/status", nil, http.Header{"Authorization": {"Bearer " + tok}})
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("the minted key does not authenticate: %d", res2.StatusCode)
	}
}
