package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The optional access password (HUNTDECK_PASSWORD).
//
// HuntDeck has no user accounts and this doesn't add any: one password
// guards the whole install, so a copy on a shared host or a public address
// shows nothing to someone who doesn't know it. Unset, nothing changes.
//
// A correct password sets one signed cookie, good for 30 days: the signing
// key is derived from the password itself, so changing the password signs
// every browser out, and no state is kept outside the browser.
const (
	sessionCookieName = "hd_session"
	sessionLifetime   = 30 * 24 * time.Hour
	// loginBudget is how many wrong passwords one address may try in
	// loginWindow before it has to wait.
	loginBudget = 8
	loginWindow = 10 * time.Minute
	// sprayBudget is how many wrong passwords may cross all addresses in
	// sprayWindow before logins pause for everyone: a distributed guess, or
	// a proxy that hides every visitor behind one address, then can't be
	// ground through. It is deliberately short — a spray slows to a crawl,
	// and an owner is never locked out for long.
	sprayBudget = 20
	sprayWindow = time.Minute
)

// auth verifies the access password and the session cookies it hands out.
type auth struct {
	key    []byte
	digest [sha256.Size]byte // the password's digest, compared in constant time
	now    func() time.Time

	mu    sync.Mutex
	fails map[string]*attempt
	spray []time.Time // every wrong password in the window, whatever the address
}

// attempt counts one address's recent wrong passwords.
type attempt struct {
	n     int
	first time.Time
}

func newAuth(password string) *auth {
	mac := hmac.New(sha256.New, []byte("huntdeck session v1"))
	mac.Write([]byte(password))
	return &auth{
		key:    mac.Sum(nil),
		digest: sha256.Sum256([]byte(password)),
		now:    time.Now,
		fails:  map[string]*attempt{},
	}
}

// check compares a submitted password in constant time.
func (a *auth) check(password string) bool {
	got := sha256.Sum256([]byte(password))
	return subtle.ConstantTimeCompare(got[:], a.digest[:]) == 1
}

// valid reports whether cookie carries a session signed with this password
// and not yet expired.
func (a *auth) valid(cookie string) bool {
	if cookie == "" {
		return false
	}
	payload, sig, ok := strings.Cut(cookie, ".")
	if !ok {
		return false
	}
	if !hmac.Equal([]byte(sig), []byte(a.sign(payload))) {
		return false
	}
	parts := strings.Split(payload, "|")
	if len(parts) != 3 || parts[0] != "v1" {
		return false
	}
	issued, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return false
	}
	at := time.Unix(issued, 0)
	if at.After(a.now().Add(time.Minute)) { // a clock a little ahead is fine
		return false
	}
	return a.now().Sub(at) < sessionLifetime
}

// token mints a session cookie value.
func (a *auth) token() string {
	var nonce [9]byte
	_, _ = rand.Read(nonce[:])
	payload := "v1|" + strconv.FormatInt(a.now().Unix(), 10) + "|" + base64.RawURLEncoding.EncodeToString(nonce[:])
	return payload + "." + a.sign(payload)
}

func (a *auth) sign(payload string) string {
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// tooMany reports whether ip has spent its login budget.
func (a *auth) tooMany(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.fails[ip]
	if f == nil {
		return false
	}
	if a.now().Sub(f.first) > loginWindow {
		delete(a.fails, ip)
		return false
	}
	return f.n >= loginBudget
}

// spraying reports whether wrong passwords are arriving faster than anyone
// could be mistyping: logins pause for a minute or so, whatever the address.
func (a *auth) spraying() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneSpray()
	return len(a.spray) >= sprayBudget
}

// pruneSpray drops failures older than the window; the caller holds the lock.
func (a *auth) pruneSpray() {
	cut := a.now().Add(-sprayWindow)
	keep := a.spray[:0]
	for _, t := range a.spray {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	a.spray = keep
}

// maxBudgets bounds the attempts map: a scan that invents an address per
// guess would otherwise grow it for as long as it runs.
const maxBudgets = 4096

// failed records a wrong password for ip.
func (a *auth) failed(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.spray = append(a.spray, now)
	if f := a.fails[ip]; f != nil && now.Sub(f.first) <= loginWindow {
		f.n++
		return
	}
	a.evictBudgets(now)
	a.fails[ip] = &attempt{n: 1, first: now}
}

// evictBudgets keeps the attempts map bounded: expired entries go first, then
// the oldest, so the newest attempts (the ones that matter) are kept. The
// caller holds the lock.
func (a *auth) evictBudgets(now time.Time) {
	if len(a.fails) < maxBudgets {
		return
	}
	var oldest string
	var oldestAt time.Time
	for ip, f := range a.fails {
		if now.Sub(f.first) > loginWindow {
			delete(a.fails, ip)
			continue
		}
		if oldest == "" || f.first.Before(oldestAt) {
			oldest, oldestAt = ip, f.first
		}
	}
	if len(a.fails) >= maxBudgets && oldest != "" {
		delete(a.fails, oldest)
	}
}

// succeeded forgets ip's wrong passwords.
func (a *auth) succeeded(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.fails, ip)
}

// loginOpenPaths are served without a session: the login page itself, the
// language switch, static files, the health check, and the files a crawler is
// entitled to read.
//
// robots.txt matters most of the three: behind the login it answered 303, so a
// crawler followed the redirect and read the login page instead of the
// "Disallow: /" that tells it to stay away.
func loginOpenPaths(path string) bool {
	switch path {
	case "/login", "/healthz", "/favicon.ico", "/settings/language",
		"/apple-touch-icon.png", "/apple-touch-icon-precomposed.png",
		"/robots.txt", "/sitemap.xml", "/.well-known/security.txt":
		return true
	}
	return strings.HasPrefix(path, "/static/")
}

// middleware turns away requests without a session.
func (a *auth) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if loginOpenPaths(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie(sessionCookieName); err == nil && a.valid(c.Value) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized: this HuntDeck asks for its access password")
			return
		}
		next := r.URL.RequestURI()
		http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusSeeOther)
	})
}

// setSession hands the browser a fresh session cookie. It is Secure whenever
// the request came over TLS, directly or through a proxy that says so.
func setSession(w http.ResponseWriter, r *http.Request, value string) {
	//nolint:gosec // Secure follows the request scheme: a copy reached over plain HTTP on localhost must still sign in
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   int(sessionLifetime / time.Second),
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSession expires the cookie.
func clearSession(w http.ResponseWriter, r *http.Request) {
	//nolint:gosec // Secure follows the request scheme: a copy reached over plain HTTP on localhost must still sign in
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// requestIsHTTPS reports whether the browser reached us over TLS, directly
// or through a proxy that sets X-Forwarded-Proto.
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// clientIP is the address a login budget counts against: the proxy's idea of
// the visitor when HUNTDECK_CLIENT_IP_HEADER is set, else the socket's peer.
// clientIP is what a login budget is counted against. An IPv6 host owns a
// whole /64 and could otherwise mint a fresh address per attempt, so the
// prefix is the unit, as it is for the visitor limiter.
func (s *server) clientIP(r *http.Request) string {
	host := ""
	if s.ClientIPHeader != "" {
		host = strings.TrimSpace(r.Header.Get(s.ClientIPHeader))
	}
	if host == "" {
		if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			host = h
		} else {
			host = r.RemoteAddr
		}
	}
	return budgetKey(host)
}

// budgetKey reduces an address to the unit its budget is counted in: the
// IPv4 address, or the /64 an IPv6 address belongs to.
func budgetKey(host string) string {
	host = strings.TrimSpace(host)
	// A bracketed address with a port has to be split first: "[::1]:443".
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// safeNext only allows a redirect back into this site: a path, not another
// host, so a crafted ?next= can't send a visitor elsewhere.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	if u, err := url.Parse(next); err != nil || u.Host != "" || u.Scheme != "" {
		return "/"
	}
	return next
}

// tokenGate accepts `Authorization: Bearer <token>` as another way to pass the
// single access gate, for a script that has no browser to log in with. A token
// is one key to the same lock: it grants what the password grants, and revoking
// it takes nothing else away.
func (s *server) tokenGate(open, guarded http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			guarded.ServeHTTP(w, r)
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if token == "" || s.Store == nil || s.Store.CheckAPIToken(r.Context(), token) != nil {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized: that token is unknown or revoked")
			return
		}
		open.ServeHTTP(w, r)
	})
}
