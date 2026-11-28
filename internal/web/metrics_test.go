package web

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The middleware counts what it served, by route and status, and the scrape
// reports it in the exposition format.
func TestMetricsCountAndRender(t *testing.T) {
	m := newMetrics()
	h := m.middleware(nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hi"))
	}))
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Pattern = "GET /x"
		h.ServeHTTP(rec, req)
	}
	out := m.prometheusText(nil)
	for _, want := range []string{
		`huntdeck_requests_total{route="GET /x",status=418} 3`,
		`huntdeck_request_duration_seconds_count{route="GET /x"} 3`,
		"huntdeck_uptime_seconds",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("scrape lacks %q\n%s", want, out)
		}
	}
}

func TestStatusRecorderHijackerAndFlusher(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	m := newMetrics()
	srv := httptest.NewServer(m.middleware(log, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f, ok := w.(http.Flusher); !ok {
			t.Fatal("statusRecorder does not implement http.Flusher")
		} else {
			_ = f
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("statusRecorder does not implement http.Hijacker")
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			t.Fatalf("Hijack() = %v", err)
		}
		defer func() { _ = conn.Close() }()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
	})))
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/novnc/websockify", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", res.StatusCode)
	}
	if strings.Contains(logs.String(), "slow or failing request") {
		t.Errorf("unexpected slow/failing log for 101 hijack: %s", logs.String())
	}
}
