package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// HUNTDECK_TOKEN is the credential for a script: the page offers it, so the
// client has to present it. Without this the config the page printed ended in
// errUnauthorized on every tool call.
func TestTokenIsPresentedAsABearerHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		got = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"programs":1,"platforms":["hackerone"]}`))
	}))
	defer srv.Close()

	s := New(srv.URL, "", "test", false)
	s.Token = "hs_test"
	out, err := s.callTool(context.Background(), "huntdeck_status", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Bearer hs_test") {
		t.Fatalf("Authorization = %q", got)
	}
	var v any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("the answer is not JSON: %v", err)
	}
}
