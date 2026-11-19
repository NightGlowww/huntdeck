package web

import (
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
