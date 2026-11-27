package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestBufferRingFilterAndRedaction(t *testing.T) {
	var stderr bytes.Buffer
	log, buf := NewWithBuffer(&stderr, slog.LevelDebug, "text", 3)

	log.With("platform", "vulbox").Info("first message", "cookie", "vulbox_token=supersecret123")
	log.WithGroup("http").Warn("second auth warning", "authorization", "Bearer secret-jwt-token-xyz", "status", 429)
	log.Error("third error message", "detail", "Authorization: Bearer abcdefgh12345678")
	log.Debug("fourth debug message", "note", "ok")

	// Capacity 3 overwrote "first message".
	entries, head := buf.Query(Query{Limit: 10})
	if head != 4 {
		t.Fatalf("head = %d, want 4", head)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}
	if entries[0].Message != "fourth debug message" || entries[2].Message != "second auth warning" {
		t.Fatalf("unexpected ring order: %+v", entries)
	}

	// Secret redaction check across attrs and inline bearer values.
	for _, e := range entries {
		for _, a := range e.Attrs {
			if strings.Contains(a.Value, "secret-jwt-token-xyz") || strings.Contains(a.Value, "abcdefgh12345678") {
				t.Fatalf("secret leaked in attr %+v", a)
			}
		}
	}
	if !strings.Contains(stderr.String(), Redacted) {
		t.Fatalf("expected stderr to also redact sensitive values, got %q", stderr.String())
	}

	// Filter by level and text.
	errs, _ := buf.Query(Query{Level: "error", Limit: 10})
	if len(errs) != 1 || errs[0].Level != "error" {
		t.Fatalf("expected 1 error entry, got %+v", errs)
	}
	warns, _ := buf.Query(Query{Text: "http.status", Limit: 10})
	if len(warns) != 1 || warns[0].Message != "second auth warning" {
		t.Fatalf("expected group attr search hit, got %+v", warns)
	}

	// SinceSeq filter.
	newer, _ := buf.Query(Query{SinceSeq: 3, Limit: 10})
	if len(newer) != 1 || newer[0].Seq != 4 {
		t.Fatalf("SinceSeq=3 got %+v", newer)
	}

	counts := buf.Counts()
	if counts.Total != 3 || counts.Error != 1 || counts.Warn != 1 || counts.Debug != 1 {
		t.Fatalf("unexpected counts %+v", counts)
	}

	buf.Clear()
	if got, _ := buf.Query(Query{Limit: 10}); len(got) != 0 {
		t.Fatalf("expected empty after Clear, got %d", len(got))
	}
}
