package web

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// Settings' Browser tab: the one Chrome website logins share
// (weblogin.Host), shown live on the page when VNC is on, with a tab per
// platform to bring forward and see its session, or finish a login by
// hand.

// LoginBrowser is the shared login browser.
type LoginBrowser interface {
	// Show brings a platform's tab to the front, starting the browser and
	// opening the tab at its page if need be, with the session HuntDeck
	// uses.
	Show(ctx context.Context, tab string) error
	// Close ends the browser; it starts again when next needed. While a
	// login runs it returns weblogin.ErrLoginRunning.
	Close() error
	Running() bool
	// Tabs are the platforms that log in in it.
	Tabs() []string
}

// browserData is the Browser tab.
type browserData struct {
	Live    bool // VNC is on: the screen shows on the page
	Running bool
	Tabs    []browserTabRow
}

type browserTabRow struct {
	Name  string
	Login *signIn // the website login's line, from Polling's
}

// browserTab gathers the Browser tab from the platforms' rows.
func (s *server) browserTab(rows []platformRow) *browserData {
	var d *browserData
	var tabs []string
	switch {
	case s.hasBrowser():
		d, tabs = &browserData{Live: s.VNC != nil, Running: s.Browser.Running()}, s.Browser.Tabs()
	case s.ReadOnly: // a tour: every platform has its tab there, none running
		d = &browserData{}
		for _, r := range rows {
			tabs = append(tabs, r.Name)
		}
	default:
		return nil
	}
	for _, name := range tabs {
		row := browserTabRow{Name: name}
		if i := slices.IndexFunc(rows, func(r platformRow) bool { return r.Name == name }); i >= 0 {
			for j := range rows[i].SignIn {
				if l := rows[i].SignIn[j]; l.NameKey == "Website login" {
					l.Watch = "" // the link here
					row.Login = &l
				}
			}
		}
		d.Tabs = append(d.Tabs, row)
	}
	return d
}

// hasBrowser reports whether there's a login browser with a platform
// logging in in it.
func (s *server) hasBrowser() bool { return s.Browser != nil && len(s.Browser.Tabs()) > 0 }

// browserShow brings a platform's tab to the front.
func (s *server) browserShow(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("platform")
	switch {
	case s.Browser == nil:
		s.browserDone(w, r, http.StatusNotFound, "browser-none")
	case !slices.Contains(s.Browser.Tabs(), name):
		s.browserDone(w, r, http.StatusNotFound, "browser-unknown")
	default:
		err := s.Browser.Show(r.Context(), name)
		if err != nil {
			s.Log.Error("login browser: show tab", "platform", name, "err", err)
			s.browserDone(w, r, http.StatusInternalServerError, "browser-failed")
			return
		}
		s.browserDone(w, r, http.StatusOK, "")
	}
}

// browserClose ends the browser.
func (s *server) browserClose(w http.ResponseWriter, r *http.Request) {
	if s.Browser == nil {
		s.browserDone(w, r, http.StatusNotFound, "browser-none")
		return
	}
	if err := s.Browser.Close(); err != nil {
		if errors.Is(err, weblogin.ErrLoginRunning) {
			s.browserDone(w, r, http.StatusConflict, "browser-busy")
			return
		}
		s.Log.Error("login browser: close", "err", err)
		s.browserDone(w, r, http.StatusInternalServerError, "browser-failed")
		return
	}
	s.browserDone(w, r, http.StatusOK, "")
}

// browserReply is the answer to the Browser tab's script.
type browserReply struct {
	Running bool   `json:"running"`
	Error   string `json:"error,omitempty"`
}

// browserDone answers the tab's script with JSON, or a form post (no
// script) by going back to the tab, with errKey's message if it failed.
func (s *server) browserDone(w http.ResponseWriter, r *http.Request, status int, errKey string) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		reply := browserReply{Error: flashErrors[errKey]}
		if s.Browser != nil {
			reply.Running = s.Browser.Running()
		}
		writeJSON(w, status, reply)
		return
	}
	to := "/settings?tab=browser"
	if errKey != "" {
		to += "&err=" + errKey
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}
