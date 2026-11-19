package web

import (
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A panic is a logged 500, not a dropped connection: the page says something
// went wrong, the API says so in JSON, and the log carries the route.
func TestPanicBecomesA500(t *testing.T) {
	var logged strings.Builder
	s := &server{Deps: Deps{Log: slog.New(slog.NewTextHandler(&logged, nil))}}
	s.pages = map[string]map[string]*template.Template{}

	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("kaboom") })
	h := s.recoverer(boom)

	// A page.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/programs", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("page panic: %d", rec.Code)
	}
	if !strings.Contains(logged.String(), "kaboom") || !strings.Contains(logged.String(), "/programs") {
		t.Errorf("the panic was not logged with its path: %s", logged.String())
	}

	// The API.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/programs", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "internal error") {
		t.Errorf("api panic: %d %q", rec.Code, rec.Body.String())
	}
}
