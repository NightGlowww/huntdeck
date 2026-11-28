package web

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

// VNC is the login browsers' display (see browser.Shared or
// browser.OnDemandVNC), which noVNC at /novnc/ shows, so a login that
// needs a person (a CAPTCHA, a new-device check) can be watched and finished
// by hand. Like the rest of HuntDeck, it's for whoever can reach the UI:
// the VNC server itself only listens on the loopback interface, behind this
// proxy.
type VNC struct {
	Addr   string // fixed VNC server address when Persistent
	Dir    string // the noVNC web client's files
	Attach func(ctx context.Context) (addr string, release func(), err error)
}

// novncPage opens noVNC on the login display, scaled to the window.
const novncPage = "/novnc/vnc.html?autoconnect=1&resize=scale&reconnect=1"

// inlineScript finds vnc.html's inline module script, which the CSP has
// to allow by its hash (scripts are otherwise 'self' only).
var inlineScript = regexp.MustCompile(`(?s)<script type="module">(.*?)</script>`)

// routeVNC serves noVNC and its websocket, if VNC is on.
func (s *server) routeVNC(mux *http.ServeMux) {
	if s.VNC == nil {
		return
	}
	csp := ""
	if page, err := os.ReadFile(filepath.Join(s.VNC.Dir, "vnc.html")); err == nil {
		var hashes []string
		for _, m := range inlineScript.FindAllSubmatch(page, -1) {
			sum := sha256.Sum256(m[1])
			hashes = append(hashes, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
		}
		csp = "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self' " + strings.Join(hashes, " ") +
			"; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"
	} else {
		s.Log.Warn("noVNC isn't installed; /novnc/ can't show the login display", "dir", s.VNC.Dir)
	}
	files := http.StripPrefix("/novnc/", http.FileServer(http.Dir(s.VNC.Dir)))
	mux.Handle("GET /novnc/websockify", s.vncSocket())
	mux.HandleFunc("GET /novnc/", func(w http.ResponseWriter, r *http.Request) {
		switch p := path.Clean(r.URL.Path); {
		case csp == "":
			http.Error(w, "noVNC isn't installed (HUNTDECK_NOVNC_DIR)", http.StatusServiceUnavailable)
			return
		case p == "/novnc":
			http.Redirect(w, r, novncPage, http.StatusFound)
			return
		case strings.HasSuffix(r.URL.Path, "/"):
			http.NotFound(w, r) // no directory listings
			return
		case p == "/novnc/vnc.html":
			w.Header().Set("Content-Security-Policy", csp)
		}
		files.ServeHTTP(w, r)
	})
}

// vncSocket carries noVNC's websocket to the VNC server. Only pages of
// HuntDeck itself may open it: the browser sends any site's page here if
// asked to, with that site as its Origin.
func (s *server) vncSocket() http.Handler {
	fixedAddr := s.VNC.Addr
	attach := s.VNC.Attach
	return websocket.Server{
		Handshake: func(cfg *websocket.Config, r *http.Request) error {
			// The browser says whether the page is HuntDeck's own (as for
			// http.CrossOriginProtection); without that, the Origin must be
			// the host asked for.
			origin, err := websocket.Origin(cfg, r)
			sameOrigin := r.Header.Get("Sec-Fetch-Site") == "same-origin" ||
				(err == nil && origin != nil && strings.EqualFold(origin.Host, r.Host) && r.Header.Get("Sec-Fetch-Site") == "")
			if !sameOrigin {
				return errors.New("novnc: cross-origin websocket refused")
			}
			if slices.Contains(cfg.Protocol, "binary") {
				cfg.Protocol = []string{"binary"}
			} else {
				cfg.Protocol = nil
			}
			return nil
		},
		Handler: func(ws *websocket.Conn) {
			defer func() { _ = ws.Close() }()
			// The server's write timeout was set on the connection before it
			// became a websocket; a session lasts as long as it's watched.
			_ = ws.SetDeadline(time.Time{})
			ws.PayloadType = websocket.BinaryFrame
			addr := fixedAddr
			if attach != nil {
				var release func()
				var err error
				addr, release, err = attach(ws.Request().Context())
				if err != nil {
					if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "not running on a display") {
						s.Log.Debug("novnc: attach skipped", "err", err)
					} else {
						s.Log.Warn("novnc: attach failed", "err", err)
					}
					return
				}
				if release != nil {
					defer release()
				}
			}
			d := net.Dialer{Timeout: 5 * time.Second}
			c, err := d.DialContext(ws.Request().Context(), "tcp", addr)
			if err != nil {
				s.Log.Warn("novnc: VNC server unreachable", "err", err)
				return
			}
			defer func() { _ = c.Close() }()
			done := make(chan struct{})
			go func() {
				_, _ = io.Copy(c, ws)
				_ = c.Close()
				close(done)
			}()
			_, _ = io.Copy(ws, c)
			_ = ws.Close()
			<-done
		},
	}
}
