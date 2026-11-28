package web

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestNoVNC(t *testing.T) {
	// A VNC server that echoes, and a noVNC page with an inline script.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	dir := t.TempDir()
	const script = `import UI from "./app/ui.js";`
	if err := os.WriteFile(filepath.Join(dir, "vnc.html"), []byte(`<html><script type="module">`+script+`</script></html>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o700); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(Deps{Log: slog.New(slog.DiscardHandler), Metrics: NewMetrics(), VNC: &VNC{Addr: ln.Addr().String(), Dir: dir}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	res, err := noRedirect.Get(srv.URL + "/novnc/vnc.html")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	sum := sha256.Sum256([]byte(script))
	if csp := res.Header.Get("Content-Security-Policy"); res.StatusCode != http.StatusOK ||
		!strings.Contains(csp, "script-src 'self' 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") || strings.Contains(csp, "unsafe") {
		t.Errorf("vnc.html: %d, CSP %q; want its inline script allowed by hash only", res.StatusCode, csp)
	}
	for path, want := range map[string]int{"/novnc/": http.StatusFound, "/novnc/app/": http.StatusNotFound} {
		res, err := noRedirect.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, res.StatusCode, want)
		}
	}

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/novnc/websockify"
	if _, err := websocket.Dial(wsURL, "binary", "https://evil.example"); err == nil {
		t.Error("another site's page opened the VNC websocket")
	}
	cfg, _ := websocket.NewConfig(wsURL, srv.URL) // a same-host Origin, but the browser says cross-site
	cfg.Protocol, cfg.Header = []string{"binary"}, http.Header{"Sec-Fetch-Site": {"cross-site"}}
	if _, err := websocket.DialConfig(cfg); err == nil {
		t.Error("a cross-site page opened the VNC websocket")
	}
	ws, err := websocket.Dial(wsURL, "binary", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.Close() }()
	ws.PayloadType = websocket.BinaryFrame
	if _, err := ws.Write([]byte("RFB 003.008\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 12)
	if _, err := io.ReadFull(ws, buf); err != nil || string(buf) != "RFB 003.008\n" {
		t.Errorf("through the websocket: %q, %v; want the VNC server's bytes", buf, err)
	}
}

func TestNoVNCOnDemandAttach(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "vnc.html"), []byte(`<html></html>`), 0o600)

	var attached, released atomic.Int32
	failAttach := false
	h, err := NewHandler(Deps{
		Log:     slog.New(slog.DiscardHandler),
		Metrics: NewMetrics(),
		VNC: &VNC{
			Dir: dir,
			Attach: func(context.Context) (string, func(), error) {
				if failAttach {
					return "", nil, errors.New("browser closed")
				}
				attached.Add(1)
				return ln.Addr().String(), func() { released.Add(1) }, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/novnc/websockify"
	ws, err := websocket.Dial(wsURL, "binary", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ws.PayloadType = websocket.BinaryFrame
	if _, err := ws.Write([]byte("RFB 003.008\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 12)
	if _, err := io.ReadFull(ws, buf); err != nil || string(buf) != "RFB 003.008\n" {
		t.Fatalf("read = %q, %v", buf, err)
	}
	_ = ws.Close()

	deadline := time.Now().Add(time.Second)
	for released.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if attached.Load() != 1 || released.Load() != 1 {
		t.Errorf("attached=%d released=%d, want 1 and 1", attached.Load(), released.Load())
	}

	failAttach = true
	ws2, err := websocket.Dial(wsURL, "binary", srv.URL)
	if err == nil {
		var tmp [1]byte
		_, rerr := ws2.Read(tmp[:])
		_ = ws2.Close()
		if rerr == nil {
			t.Error("expected closed websocket when Attach fails")
		}
	}
}
