package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An agent told "is the password right?" when the port is closed chases the
// wrong thing: the two failures have to read differently.
func TestFailuresReadDifferently(t *testing.T) {
	// A closed port: nothing listens.
	closed := httptest.NewServer(http.NotFoundHandler())
	addr := closed.URL
	closed.Close()
	s := New(addr, "pw", "test", false)
	_, err := s.callTool(context.Background(), "huntdeck_status", nil)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("closed port reads as: %v", err)
	}

	// A wrong password: the instance answers, and refuses.
	srv := instance(t, "right")
	bad := New(srv.URL, "wrong", "test", false)
	_, err = bad.callTool(context.Background(), "huntdeck_status", nil)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("wrong password reads as: %v", err)
	}
}
