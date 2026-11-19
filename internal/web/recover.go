package web

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
)

// recoverer turns a panic into a logged 500 instead of a dropped connection:
// net/http's own recovery closes the socket and says nothing, which looks like
// a network fault to the person and leaves no trace in the log.
func (s *server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &wroteRecorder{ResponseWriter: w}
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			s.Log.Error("panic serving request", "method", r.Method, "path", r.URL.Path,
				"route", r.Pattern, "panic", fmt.Sprint(v), "stack", string(debug.Stack()))
			if rec.wrote {
				// Something of the response is already on the wire: the only
				// honest thing left is to cut it there.
				return
			}
			if strings.HasPrefix(r.URL.Path, "/api/") || r.Pattern == "GET /api/v1/openapi.json" {
				writeJSONError(rec, http.StatusInternalServerError, "internal error")
				return
			}
			s.renderError(rec, r, http.StatusInternalServerError, "Something went wrong.")
		}()
		next.ServeHTTP(rec, r)
	})
}

// wroteRecorder remembers whether the response has started, so a panic after
// the first byte does not try to render a page over it.
type wroteRecorder struct {
	http.ResponseWriter
	wrote bool
}

func (w *wroteRecorder) WriteHeader(code int) {
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *wroteRecorder) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

// Hijack lets a handler take the connection (the VNC websocket does): a
// wrapper that hides it turns every upgrade into an unexpected EOF.
func (w *wroteRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("%T cannot hijack", w.ResponseWriter)
	}
	w.wrote = true
	return hj.Hijack()
}

// Flush keeps streaming responses streaming.
func (w *wroteRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		w.wrote = true
		f.Flush()
	}
}
