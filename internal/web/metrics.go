package web

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// metrics is the smallest thing that answers "what is slow and what is
// failing" from the outside: per-route counts and a latency histogram. It is
// in-process and bounded (one entry per route pattern, not per URL), because
// this app runs in one container with no collector beside it.
type metrics struct {
	mu     sync.Mutex
	routes map[string]*routeStat
	start  time.Time
}

type routeStat struct {
	count   int64
	byCode  map[int]int64
	millis  []float64 // capped sample of durations for percentiles
	over500 int64
}

// sampleLimit bounds the durations kept per route: percentiles from the last
// few thousand requests are enough, and memory stays flat under load.
const sampleLimit = 2048

// latencyBuckets are the histogram's upper bounds, in milliseconds.
var latencyBuckets = []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500}

// NewMetrics is the zero-value-with-mutex metrics set a server records into.
func NewMetrics() *metrics {
	return newMetrics()
}

func newMetrics() *metrics {
	return &metrics{routes: map[string]*routeStat{}, start: time.Now()}
}

// statusRecorder remembers what a handler wrote.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

// Hijack lets websocket handlers (such as noVNC) take over the connection.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("%T cannot hijack", r.ResponseWriter)
	}
	if r.status == 0 {
		r.status = http.StatusSwitchingProtocols
	}
	return hj.Hijack()
}

// Flush forwards streaming flushes to the underlying ResponseWriter.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the wrapped ResponseWriter for http.ResponseController.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// middleware times and counts one request, and logs the slow and the failing.
func (m *metrics) middleware(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		took := time.Since(start)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		pattern := r.Pattern
		if pattern == "" {
			pattern = "unmatched"
		}
		m.record(pattern, rec.status, took)
		if rec.status != http.StatusSwitchingProtocols && (rec.status >= 500 || took > 500*time.Millisecond) {
			log.Warn("slow or failing request", "method", r.Method, "route", pattern,
				"status", rec.status, "took", took.Round(time.Millisecond), "bytes", rec.bytes)
		}
	})
}

func (m *metrics) record(route string, code int, took time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.routes[route]
	if st == nil {
		st = &routeStat{byCode: map[int]int64{}}
		m.routes[route] = st
	}
	st.count++
	st.byCode[code]++
	ms := float64(took.Microseconds()) / 1000
	if len(st.millis) < sampleLimit {
		st.millis = append(st.millis, ms)
	}
	if took > 500*time.Millisecond {
		st.over500++
	}
}

// prometheusText renders the counters and histograms in the exposition format.
func (m *metrics) prometheusText(extra []string) string {
	m.mu.Lock()
	routes := make([]string, 0, len(m.routes))
	for route := range m.routes {
		routes = append(routes, route)
	}
	sort.Strings(routes)
	type snap struct {
		count   int64
		byCode  map[int]int64
		millis  []float64
		over500 int64
	}
	snaps := make(map[string]snap, len(routes))
	for _, route := range routes {
		st := m.routes[route]
		cp := make([]float64, len(st.millis))
		copy(cp, st.millis)
		codes := make(map[int]int64, len(st.byCode))
		for k, v := range st.byCode {
			codes[k] = v
		}
		snaps[route] = snap{st.count, codes, cp, st.over500}
	}
	uptime := time.Since(m.start)
	m.mu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "# HELP huntdeck_uptime_seconds Seconds since this process started.\n")
	fmt.Fprintf(&b, "# TYPE huntdeck_uptime_seconds gauge\nhuntdeck_uptime_seconds %.0f\n", uptime.Seconds())
	b.WriteString("# HELP huntdeck_requests_total Requests handled, by route and status.\n# TYPE huntdeck_requests_total counter\n")
	for _, route := range routes {
		s := snaps[route]
		codes := make([]int, 0, len(s.byCode))
		for c := range s.byCode {
			codes = append(codes, c)
		}
		sort.Ints(codes)
		for _, c := range codes {
			fmt.Fprintf(&b, "huntdeck_requests_total{route=%q,status=%d} %d\n", route, c, s.byCode[c])
		}
	}
	b.WriteString("# HELP huntdeck_request_duration_seconds Request latency, cumulative buckets.\n# TYPE huntdeck_request_duration_seconds histogram\n")
	for _, route := range routes {
		s := snaps[route]
		for _, bound := range latencyBuckets {
			n := 0
			for _, ms := range s.millis {
				if ms <= bound {
					n++
				}
			}
			fmt.Fprintf(&b, "huntdeck_request_duration_seconds_bucket{route=%q,le=\"%g\"} %d\n", route, bound/1000, n)
		}
		fmt.Fprintf(&b, "huntdeck_request_duration_seconds_bucket{route=%q,le=\"+Inf\"} %d\n", route, len(s.millis))
		fmt.Fprintf(&b, "huntdeck_request_duration_seconds_count{route=%q} %d\n", route, s.count)
	}
	b.WriteString("# HELP huntdeck_requests_over_500ms_total Requests that took longer than half a second.\n# TYPE huntdeck_requests_over_500ms_total counter\n")
	for _, route := range routes {
		fmt.Fprintf(&b, "huntdeck_requests_over_500ms_total{route=%q} %d\n", route, snaps[route].over500)
	}
	for _, line := range extra {
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// metricsPage renders the counters for a scrape. It sits behind the access
// password like every other page: the numbers are not secret, but where the
// app is reachable is the user's decision.
func (s *server) metricsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, s.Metrics.prometheusText(s.appMetrics(r)))
}

// appMetrics gathers the few gauges that say whether HuntDeck is actually
// working: how fresh each platform's poll is, how old the queue is, how much
// notification mail is waiting, and how fast the database answers.
func (s *server) appMetrics(r *http.Request) []string {
	ctx := r.Context()
	var out []string
	if s.DB != nil {
		start := time.Now()
		if err := s.DB.Ping(ctx); err == nil {
			out = append(out, fmt.Sprintf("huntdeck_db_ping_seconds %.4f", time.Since(start).Seconds()))
		} else {
			out = append(out, "huntdeck_db_ping_seconds NaN")
		}
	}
	if checked, err := s.Store.PlatformsChecked(ctx); err == nil {
		out = append(out, "# HELP huntdeck_platform_checked_timestamp_seconds When the platform was last read.")
		out = append(out, "# TYPE huntdeck_platform_checked_timestamp_seconds gauge")
		for _, c := range checked {
			if c.At.IsZero() {
				continue
			}
			out = append(out, fmt.Sprintf("huntdeck_platform_checked_timestamp_seconds{platform=%q} %d",
				c.Platform, c.At.Unix()))
		}
	}
	if n, computed, err := s.Store.HuntQueueCount(ctx, store.QueueFilter{}); err == nil {
		out = append(out, "# HELP huntdeck_queue_rows Ranked targets in the queue.")
		out = append(out, "# TYPE huntdeck_queue_rows gauge")
		out = append(out, fmt.Sprintf("huntdeck_queue_rows %d", n))
		if !computed.IsZero() {
			out = append(out, "# HELP huntdeck_queue_computed_timestamp_seconds When the queue was last rebuilt.")
			out = append(out, "# TYPE huntdeck_queue_computed_timestamp_seconds gauge")
			out = append(out, fmt.Sprintf("huntdeck_queue_computed_timestamp_seconds %d", computed.Unix()))
		}
	}
	if n, err := s.Store.NotificationBacklog(ctx); err == nil {
		out = append(out, "# HELP huntdeck_notify_backlog Notifications waiting to be delivered.")
		out = append(out, "# TYPE huntdeck_notify_backlog gauge")
		out = append(out, fmt.Sprintf("huntdeck_notify_backlog %d", n))
	}
	return out
}
