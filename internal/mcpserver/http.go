package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
)

// HTTPHandler answers the streamable-HTTP MCP transport: one POST per
// JSON-RPC message, one JSON answer, no session. A client that lives in
// another process or on another machine — Claude Desktop, an agent picker —
// connects this way instead of spawning the stdio binary.
func (s *Server) HTTPHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(response{JSONRPC: "2.0", Error: &rpcError{
				Code: -32000, Message: "Method not allowed (stateless mode: POST only)"}})
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(response{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "read error"}})
			return
		}
		var req request
		if err := json.Unmarshal(body, &req); err != nil {
			_ = json.NewEncoder(w).Encode(response{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
			return
		}
		res, err := s.handle(r.Context(), req)
		if err != nil {
			res = &response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32603, Message: err.Error()}}
		}
		if res == nil {
			// A notification: acknowledged, nothing to answer.
			w.WriteHeader(http.StatusAccepted)
			return
		}
		res.JSONRPC = "2.0"
		if len(req.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return // a notification: JSON-RPC forbids answering it
		}
		if len(res.ID) == 0 {
			res.ID = req.ID
		}
		_ = json.NewEncoder(w).Encode(res)
	}
}
