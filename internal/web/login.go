package web

import (
	"net/http"
)

// The login page: HUNTDECK_PASSWORD's only door (auth.go). It is a page of
// its own, without the sidebar or the top bar, since nothing of HuntDeck is
// visible before signing in.

// loginData is what the login page shows.
type loginData struct {
	Next  string // where to go after signing in (a path on this site)
	Error string // a wrong password or too many attempts, translated
}

func (s *server) loginPage(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	if s.auth == nil { // no password set: there is nothing to sign in to
		//nolint:gosec // next is safeNext's: a path on this site, never another host
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	if c, err := r.Cookie(sessionCookieName); err == nil && s.auth.valid(c.Value) {
		//nolint:gosec // next is safeNext's: a path on this site, never another host
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	s.renderLogin(w, r, http.StatusOK, loginData{Next: next})
}

func (s *server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	// The budget is spent before the body is read, and the body is read with a
	// limit: an unauthenticated request should not be able to make this route
	// parse a large form, however many times it asks.
	ip := s.clientIP(r)
	next := safeNext(r.URL.Query().Get("next"))
	if s.auth.tooMany(ip) || s.auth.spraying() {
		s.renderLogin(w, r, http.StatusTooManyRequests, loginData{
			Next:  next,
			Error: s.T(r, "Too many attempts. Wait a few minutes, then try again."),
		})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusRequestEntityTooLarge, "That request is too large.")
		return
	}
	if v := safeNext(r.PostFormValue("next")); v != "" {
		next = v
	}
	if !s.auth.check(r.PostFormValue("password")) {
		s.auth.failed(ip)
		s.renderLogin(w, r, http.StatusUnauthorized, loginData{
			Next:  next,
			Error: s.T(r, "Wrong password. Try again."),
		})
		return
	}
	s.auth.succeeded(ip)
	setSession(w, r, s.auth.token())
	//nolint:gosec // next is safeNext's: a path on this site, never another host
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	clearSession(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *server) renderLogin(w http.ResponseWriter, r *http.Request, status int, d loginData) {
	s.render(w, r, status, "login", pageData{Title: s.T(r, "Sign in"), Data: d})
}
