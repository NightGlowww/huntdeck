package logging

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const defaultBufferCapacity = 2000

// Attr is a single key/value attribute recorded with a log entry.
type Attr struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Entry is one sanitized runtime log record kept in the in-memory ring buffer.
type Entry struct {
	Seq     int64     `json:"seq"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	Attrs   []Attr    `json:"attrs,omitempty"`
}

// LevelCounts holds counts of retained log entries by severity.
type LevelCounts struct {
	Total int `json:"total"`
	Error int `json:"error"`
	Warn  int `json:"warn"`
	Info  int `json:"info"`
	Debug int `json:"debug"`
}

// Query filters entries read from a Buffer.
type Query struct {
	Level    string // "", "all", "error", "warn", "info", "debug"
	Text     string
	SinceSeq int64
	Limit    int
}

// Buffer is a concurrency-safe, bounded ring buffer of redacted log entries.
type Buffer struct {
	mu      sync.RWMutex
	cap     int
	nextSeq int64
	ring    []Entry
	head    int
	count   int
}

// NewBuffer returns an in-memory log ring buffer holding up to capacity entries.
func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = defaultBufferCapacity
	}
	return &Buffer{
		cap:  capacity,
		ring: make([]Entry, capacity),
	}
}

var (
	inlineBearerRE = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9\-_.~+/=]{8,}`)
	inlineCookieRE = regexp.MustCompile(`(?i)\b(vulbox_token|_bugcrowd_session|sessionid|token|secret|password)\s*=\s*[^\s;,&]{4,}`)
)

func scrubString(s string) string {
	s = inlineBearerRE.ReplaceAllString(s, "Bearer "+Redacted)
	s = inlineCookieRE.ReplaceAllString(s, "$1="+Redacted)
	return s
}

func normalizeLevel(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "err", "error":
		return "error"
	case "wrn", "warn", "warning":
		return "warn"
	case "dbg", "debug":
		return "debug"
	case "inf", "info":
		return "info"
	default:
		return "info"
	}
}

// Append adds e to the ring buffer after scrubbing any sensitive keys or values.
func (b *Buffer) Append(e Entry) {
	if b == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	} else {
		e.Time = e.Time.UTC()
	}
	e.Level = normalizeLevel(e.Level)
	e.Message = scrubString(e.Message)

	var clean []Attr
	if len(e.Attrs) > 0 {
		clean = make([]Attr, 0, len(e.Attrs))
		for _, a := range e.Attrs {
			if IsSensitiveKey(a.Key) {
				clean = append(clean, Attr{Key: a.Key, Value: Redacted})
			} else {
				clean = append(clean, Attr{Key: a.Key, Value: scrubString(a.Value)})
			}
		}
	}
	e.Attrs = clean

	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextSeq++
	e.Seq = b.nextSeq
	if b.count < b.cap {
		idx := (b.head + b.count) % b.cap
		b.ring[idx] = e
		b.count++
		return
	}
	b.ring[b.head] = e
	b.head = (b.head + 1) % b.cap
}

// Clear removes all entries while keeping the sequence counter monotonic.
func (b *Buffer) Clear() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	clear(b.ring)
	b.head = 0
	b.count = 0
}

// Counts returns severity counts over all currently retained entries.
func (b *Buffer) Counts() LevelCounts {
	if b == nil {
		return LevelCounts{}
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	var c LevelCounts
	c.Total = b.count
	for i := range b.count {
		e := b.ring[(b.head+i)%b.cap]
		switch e.Level {
		case "error":
			c.Error++
		case "warn":
			c.Warn++
		case "debug":
			c.Debug++
		default:
			c.Info++
		}
	}
	return c
}

// Query returns matching entries ordered newest-first, along with the latest sequence ID.
func (b *Buffer) Query(q Query) ([]Entry, int64) {
	if b == nil {
		return nil, 0
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	wantLevel := strings.ToLower(strings.TrimSpace(q.Level))
	if wantLevel == "all" {
		wantLevel = ""
	}
	needle := strings.ToLower(strings.TrimSpace(q.Text))

	b.mu.RLock()
	defer b.mu.RUnlock()

	headSeq := b.nextSeq
	out := make([]Entry, 0, min(limit, b.count))
	for i := b.count - 1; i >= 0 && len(out) < limit; i-- {
		e := b.ring[(b.head+i)%b.cap]
		if q.SinceSeq > 0 && e.Seq <= q.SinceSeq {
			break
		}
		if wantLevel != "" && e.Level != wantLevel {
			continue
		}
		if needle != "" && !entryMatches(e, needle) {
			continue
		}
		out = append(out, e)
	}
	return out, headSeq
}

func entryMatches(e Entry, needle string) bool {
	if strings.Contains(strings.ToLower(e.Message), needle) {
		return true
	}
	for _, a := range e.Attrs {
		if strings.Contains(strings.ToLower(a.Key), needle) || strings.Contains(strings.ToLower(a.Value), needle) {
			return true
		}
	}
	return false
}

// NewWithBuffer creates a slog.Logger writing redacted records to both w and an
// in-memory Buffer of the given capacity.
func NewWithBuffer(w io.Writer, level slog.Level, format string, capacity int) (*slog.Logger, *Buffer) {
	base := New(w, level, format)
	buf := NewBuffer(capacity)
	// Keep at least Info in the UI ring buffer even if stderr uses Warn, while honoring Debug if requested.
	bufLevel := slog.LevelInfo
	if level < bufLevel {
		bufLevel = level
	}
	tee := &teeHandler{
		primary: base.Handler(),
		buf:     buf,
		level:   bufLevel,
	}
	return slog.New(tee), buf
}

type teeHandler struct {
	primary slog.Handler
	buf     *Buffer
	level   slog.Level
	groups  []string
	attrs   []slog.Attr
}

func (h *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.primary.Enabled(ctx, level) || level >= h.level
}

func (h *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	var err error
	if h.primary.Enabled(ctx, r.Level) {
		err = h.primary.Handle(ctx, r)
	}
	if r.Level >= h.level && h.buf != nil {
		attrs := make([]Attr, 0, len(h.attrs)+r.NumAttrs())
		for _, a := range h.attrs {
			attrs = collectAttr(attrs, h.groups, a)
		}
		r.Attrs(func(a slog.Attr) bool {
			attrs = collectAttr(attrs, h.groups, a)
			return true
		})
		h.buf.Append(Entry{
			Time:    r.Time,
			Level:   r.Level.String(),
			Message: r.Message,
			Attrs:   attrs,
		})
	}
	return err
}

func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cloned := slices.Clone(h.attrs)
	for _, a := range attrs {
		cloned = append(cloned, redactAttr(a))
	}
	return &teeHandler{
		primary: h.primary.WithAttrs(attrs),
		buf:     h.buf,
		level:   h.level,
		groups:  slices.Clone(h.groups),
		attrs:   cloned,
	}
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	groups := append(slices.Clone(h.groups), name)
	return &teeHandler{
		primary: h.primary.WithGroup(name),
		buf:     h.buf,
		level:   h.level,
		groups:  groups,
		attrs:   slices.Clone(h.attrs),
	}
}

func redactAttr(a slog.Attr) slog.Attr {
	if a.Value.Kind() != slog.KindGroup && IsSensitiveKey(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	return a
}

func collectAttr(dst []Attr, groups []string, a slog.Attr) []Attr {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return dst
	}
	if a.Value.Kind() == slog.KindGroup {
		sub := a.Value.Group()
		nextGroups := groups
		if a.Key != "" {
			nextGroups = append(slices.Clone(groups), a.Key)
		}
		for _, g := range sub {
			dst = collectAttr(dst, nextGroups, g)
		}
		return dst
	}
	key := a.Key
	if len(groups) > 0 {
		key = strings.Join(append(slices.Clone(groups), a.Key), ".")
	}
	if IsSensitiveKey(key) {
		return append(dst, Attr{Key: key, Value: Redacted})
	}
	return append(dst, Attr{Key: key, Value: a.Value.String()})
}
