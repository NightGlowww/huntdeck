package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/logging"
)

func TestLogsPageFilterTailExportClearAndRedaction(t *testing.T) {
	buf := logging.NewBuffer(50)
	buf.Append(logging.Entry{
		Level:   "info",
		Message: "vulbox: browser sign-in succeeded",
		Attrs:   []logging.Attr{{Key: "token_len", Value: "128"}, {Key: "cookie", Value: "vulbox_token=secret123"}},
	})
	buf.Append(logging.Entry{
		Level:   "error",
		Message: "yeswehack: browser navigate failed",
		Attrs:   []logging.Attr{{Key: "err", Value: "net::ERR_CONNECTION_CLOSED"}, {Key: "authorization", Value: "Bearer topsecret123"}},
	})

	e := newTestEnv(t, func(d *Deps) { d.Logs = buf })

	// 1. Full /logs page renders entries with strict secret redaction.
	res, body := e.do(e.client(), http.MethodGet, "/logs", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /logs status %d", res.StatusCode)
	}
	if !strings.Contains(body, "vulbox: browser sign-in succeeded") || !strings.Contains(body, "yeswehack: browser navigate failed") {
		t.Fatalf("GET /logs missing expected entries: %s", body)
	}
	if strings.Contains(body, "secret123") || strings.Contains(body, "topsecret123") {
		t.Fatal("GET /logs leaked unredacted secret")
	}

	// 2. Filtered by level=error & q=yeswehack.
	res, filtered := e.do(e.client(), http.MethodGet, "/logs?level=error&q=yeswehack", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("filtered /logs status %d", res.StatusCode)
	}
	if !strings.Contains(filtered, "net::ERR_CONNECTION_CLOSED") || strings.Contains(filtered, "vulbox: browser sign-in succeeded") {
		t.Fatalf("filtered /logs did not filter accurately")
	}

	// 3. JSON tail endpoint returns structured counts & entries.
	res, tailBody := e.do(e.client(), http.MethodGet, "/logs/tail?level=error", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /logs/tail status %d", res.StatusCode)
	}
	var tail struct {
		Head    int64               `json:"head"`
		Counts  logging.LevelCounts `json:"counts"`
		Entries []logViewEntry      `json:"entries"`
	}
	if err := json.Unmarshal([]byte(tailBody), &tail); err != nil {
		t.Fatalf("decode /logs/tail: %v", err)
	}
	if tail.Counts.Total != 2 || tail.Counts.Error != 1 || len(tail.Entries) != 1 || tail.Entries[0].Badge != "ERR" {
		t.Fatalf("unexpected /logs/tail response: %+v", tail)
	}

	// 4. Plain-text export endpoint downloads filtered sanitized log text.
	res, txt := e.do(e.client(), http.MethodGet, "/logs?format=txt&level=info", nil, nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("export status=%d ctype=%q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if !strings.Contains(txt, "[INF] vulbox: browser sign-in succeeded") || strings.Contains(txt, "secret123") {
		t.Fatalf("unexpected txt export: %q", txt)
	}

	// 5. Clear endpoint empties the ring buffer and redirects cleanly.
	res, _ = e.do(e.client(), http.MethodPost, "/logs/clear", nil, nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /logs/clear status %d, want 303", res.StatusCode)
	}
	if buf.Counts().Total != 0 {
		t.Fatalf("expected buffer empty after clear, got %d", buf.Counts().Total)
	}
}

func TestLogsHiddenInReadOnly(t *testing.T) {
	buf := logging.NewBuffer(10)
	buf.Append(logging.Entry{Level: slog.LevelError.String(), Message: "internal-op-error"})
	e := newTestEnv(t, func(d *Deps) { d.ReadOnly = true; d.Logs = buf })

	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/logs", http.StatusNotFound},
		{http.MethodGet, "/logs/tail", http.StatusNotFound},
		{http.MethodPost, "/logs/clear", http.StatusForbidden},
	} {
		res, body := e.do(e.client(), tc.method, tc.path, nil, nil)
		if res.StatusCode != tc.want {
			t.Errorf("%s %s in read-only mode: status %d, want %d", tc.method, tc.path, res.StatusCode, tc.want)
		}
		if strings.Contains(body, "internal-op-error") {
			t.Errorf("%s %s leaked log message in read-only mode", tc.method, tc.path)
		}
	}
}
