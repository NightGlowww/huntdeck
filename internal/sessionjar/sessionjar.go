// Package sessionjar is a cookie jar that keeps a web login's cookies on
// disk, so a restart picks up the session where it left off instead of
// logging in again. Only the named hosts' cookies are kept; the file is
// readable by its owner only, as it holds live sessions.
package sessionjar

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Jar is an http.CookieJar that saves the named hosts' cookies to a file.
type Jar struct {
	inner *cookiejar.Jar
	path  string
	hosts map[string]bool
	now   func() time.Time

	mu    sync.Mutex
	saved map[string]stored // by key()
}

// stored is one saved cookie. A zero Expires is a session cookie.
type stored struct {
	Host     string    `json:"host"`             // the host it was set by
	Domain   string    `json:"domain,omitempty"` // set for domain cookies; else host-only
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	Expires  time.Time `json:"expires,omitzero"`
	Secure   bool      `json:"secure,omitempty"`
	HTTPOnly bool      `json:"http_only,omitempty"`
}

func (s stored) key() string { return s.Host + "\x00" + s.Domain + "\x00" + s.Path + "\x00" + s.Name }

type file struct {
	Cookies []stored `json:"cookies"`
}

// Open returns a jar holding the cookies saved at path that haven't expired.
// A missing file is an empty jar, and so is one that can't be parsed (it is
// replaced on the next save).
func Open(path string, hosts ...string) (*Jar, error) {
	inner, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	j := &Jar{inner: inner, path: path, hosts: map[string]bool{}, now: time.Now, saved: map[string]stored{}}
	for _, h := range hosts {
		j.hosts[strings.ToLower(h)] = true
	}
	raw, err := os.ReadFile(path) //nolint:gosec // the configured data directory
	switch {
	case os.IsNotExist(err):
		return j, nil
	case err != nil:
		return nil, fmt.Errorf("sessionjar: %w", err)
	}
	var f file
	if json.Unmarshal(raw, &f) != nil {
		return j, nil
	}
	now := j.now()
	for _, s := range f.Cookies {
		if !j.hosts[s.Host] || (!s.Expires.IsZero() && !s.Expires.After(now)) {
			continue
		}
		j.saved[s.key()] = s
		//nolint:gosec // restores a cookie as the site set it
		c := &http.Cookie{Name: s.Name, Value: s.Value, Domain: s.Domain, Path: s.Path, Expires: s.Expires, Secure: s.Secure, HttpOnly: s.HTTPOnly}
		j.inner.SetCookies(&url.URL{Scheme: "https", Host: s.Host, Path: "/"}, []*http.Cookie{c})
	}
	return j, nil
}

// Saved are the kept cookies set by host, as the site set them (Domain
// empty for a host-only cookie), for another browser to take up the session.
func (j *Jar) Saved(host string) []*http.Cookie {
	host = strings.ToLower(host)
	j.mu.Lock()
	defer j.mu.Unlock()
	now := j.now()
	var out []*http.Cookie
	for _, s := range j.saved {
		if s.Host != host || (!s.Expires.IsZero() && !s.Expires.After(now)) {
			continue
		}
		//nolint:gosec // a copy of the cookie as the site set it
		out = append(out, &http.Cookie{Name: s.Name, Value: s.Value, Domain: s.Domain, Path: s.Path, Expires: s.Expires, Secure: s.Secure, HttpOnly: s.HTTPOnly})
	}
	return out
}

// Hosts are the hosts whose cookies it keeps.
func (j *Jar) Hosts() []string {
	out := make([]string, 0, len(j.hosts))
	for h := range j.hosts {
		out = append(out, h)
	}
	slices.Sort(out)
	return out
}

// Cookies implements http.CookieJar.
func (j *Jar) Cookies(u *url.URL) []*http.Cookie { return j.inner.Cookies(u) }

// SetCookies implements http.CookieJar, saving the named hosts' cookies.
// A failed save leaves the cookies in memory: the session still works until
// a restart.
func (j *Jar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.inner.SetCookies(u, cookies)
	host := strings.ToLower(u.Hostname())
	if !j.hosts[host] {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	now := j.now()
	changed := false
	for _, c := range cookies {
		s := stored{Host: host, Domain: strings.TrimPrefix(strings.ToLower(c.Domain), "."), Path: c.Path,
			Name: c.Name, Value: c.Value, Secure: c.Secure, HTTPOnly: c.HttpOnly}
		if s.Path == "" || !strings.HasPrefix(s.Path, "/") {
			s.Path = defaultPath(u.Path)
		}
		gone := false
		switch {
		case c.MaxAge < 0:
			gone = true
		case c.MaxAge > 0:
			s.Expires = now.Add(time.Duration(c.MaxAge) * time.Second)
		case !c.Expires.IsZero():
			s.Expires = c.Expires
			gone = !c.Expires.After(now)
		}
		if gone {
			if _, ok := j.saved[s.key()]; ok {
				delete(j.saved, s.key())
				changed = true
			}
			continue
		}
		if old, ok := j.saved[s.key()]; !ok || old != s {
			j.saved[s.key()] = s
			changed = true
		}
	}
	if changed {
		_ = j.save()
	}
}

// defaultPath is a cookie's path when it doesn't give one (RFC 6265 5.1.4).
func defaultPath(p string) string {
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return "/"
	}
	return p[:i]
}

// save writes the saved cookies, replacing the file in one step. j.mu is held.
func (j *Jar) save() error {
	f := file{Cookies: make([]stored, 0, len(j.saved))}
	for _, s := range j.saved {
		f.Cookies = append(f.Cookies, s)
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	dir := filepath.Dir(j.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".sessionjar-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), j.path)
}

// Checkpoint is the jar's state as bytes: the cookies it holds and the file it
// would write. It exists for one caller - a login attempt that must not be able
// to damage a session that already works (see Rollback).
func (j *Jar) Checkpoint() []byte {
	j.mu.Lock()
	defer j.mu.Unlock()
	f := file{Cookies: make([]stored, 0, len(j.saved))}
	for _, s := range j.saved {
		f.Cookies = append(f.Cookies, s)
	}
	slices.SortFunc(f.Cookies, func(a, b stored) int { return strings.Compare(a.key(), b.key()) })
	b, err := json.Marshal(f)
	if err != nil {
		return nil
	}
	return b
}

// Rollback puts the jar back to a Checkpoint: memory and file, or neither.
//
// The cookie-level restore in one platform was not enough, because a login held
// in a real browser copies its cookies into this jar and the copy can land after
// the attempt has already been judged. Restoring the bytes the jar had before is
// the only version of "as it was" that cannot be raced.
func (j *Jar) Rollback(raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("sessionjar: rollback: %w", err)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.saved = make(map[string]stored, len(f.Cookies))
	inner, ierr := cookiejar.New(nil)
	if ierr != nil {
		return ierr
	}
	j.inner = inner
	for _, s := range f.Cookies {
		j.saved[s.key()] = s
		//nolint:gosec // puts back the cookie as the site set it
		c := &http.Cookie{Name: s.Name, Value: s.Value, Domain: s.Domain, Path: s.Path, Expires: s.Expires, Secure: s.Secure, HttpOnly: s.HTTPOnly}
		j.inner.SetCookies(&url.URL{Scheme: "https", Host: s.Host, Path: "/"}, []*http.Cookie{c})
	}
	return j.save()
}
