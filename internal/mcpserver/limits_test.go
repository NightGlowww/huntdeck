package mcpserver

import (
	"context"
	"strings"
	"testing"
)

// A reply over the cap is an error, never a quiet cut: an agent that saves a
// truncated export has a file that will not import and nothing saying why.
func TestReadCapped(t *testing.T) {
	small, err := readCapped(strings.NewReader("ok"), 8)
	if err != nil || string(small) != "ok" {
		t.Fatalf("small body: %q %v", small, err)
	}
	_, err = readCapped(strings.NewReader(strings.Repeat("x", 10)), 8)
	if err == nil || !strings.Contains(err.Error(), "over") {
		t.Fatalf("over the cap: %v", err)
	}
}

// Asking for an export format that does not exist says so, instead of
// quietly answering with a Burp file.
func TestExportRefusesUnknownFormat(t *testing.T) {
	srv := instance(t, "")
	s := New(srv.URL, "", "test", false)
	_, err := s.callTool(context.Background(), "huntdeck_export", map[string]any{"platform": "hackerone", "handle": "acme", "format": "pdf"})
	if err == nil || !strings.Contains(err.Error(), "not one of") {
		t.Fatalf("unknown format: %v", err)
	}
}

// The bulk matcher sends the list as the body, one per line, and refuses a
// list longer than one call takes.
func TestMatchMany(t *testing.T) {
	srv := instance(t, "pw")
	s := New(srv.URL, "pw", "test", false)
	out, err := s.callTool(context.Background(), "huntdeck_match_many", map[string]any{"hosts": "a.example.com\nb.example.com\n\nc.example.com\n"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"out":3`) {
		t.Errorf("three hosts should arrive as three lines: %s", out)
	}
	if _, err := s.callTool(context.Background(), "huntdeck_match_many", map[string]any{"hosts": "   "}); err == nil {
		t.Error("an empty list was accepted")
	}
}
