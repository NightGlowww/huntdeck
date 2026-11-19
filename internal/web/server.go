// Package web serves the HuntDeck UI and HTTP API.
package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mdpsec/huntdeck/internal/ai"
	"github.com/mdpsec/huntdeck/internal/config"

	"github.com/mdpsec/huntdeck/internal/i18n"
	"github.com/mdpsec/huntdeck/internal/overrides"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/independent"
	"github.com/mdpsec/huntdeck/internal/scheduler"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Pinger is the database health check the server depends on.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Poller starts a poll in the background.
type Poller interface {
	// PollNow returns store.ErrRunInProgress if the platform is busy and
	// ErrUnknownPlatform if it isn't configured.
	PollNow(platform, mode string) error
	// AcceptInvite accepts the invitation to a program on its platform,
	// then refreshes that program in the background. It returns
	// ErrStillAccepting if the platform is slow to answer (it carries on),
	// ErrNoInvite if there's no pending invitation, and ErrCannotAccept if
	// the platform's invitations can't be accepted from here.
	AcceptInvite(platform, handle string) error
}

// ErrUnknownPlatform means the platform has no credentials configured.
var ErrUnknownPlatform = errors.New("platform not configured")

// ErrPlatformDisabled means the platform is turned off in Settings.
var ErrPlatformDisabled = errors.New("platform disabled")

// Errors from Poller.AcceptInvite.
var (
	ErrCannotAccept   = errors.New("invitations can't be accepted from HuntDeck on this platform")
	ErrNoInvite       = errors.New("no pending invitation")
	ErrStillAccepting = errors.New("still accepting")
)

// PlatformInfo names a configured platform.
type PlatformInfo struct {
	Name string
	// Credentials reports how the platform's credentials did when last
	// used (platform.CredentialReporter); nil if it doesn't say.
	Credentials func() []platform.Credential
	// AcceptsInvites says its invitations can be accepted from HuntDeck.
	AcceptsInvites bool
	// Discloses says it publishes disclosed reports
	// (platform.DisclosureFetcher): elsewhere none isn't "0".
	Discloses bool
}

// Schedule reads and edits per-platform polling settings; see
// scheduler.Schedule.
type Schedule interface {
	Get(platform string) scheduler.Settings
	Default(platform string) scheduler.Settings
	Overridden(platform string) bool
	Set(ctx context.Context, platform string, s scheduler.Settings) error
	Reset(ctx context.Context, platform string) error
	Web(platform string) store.WebSyncSettings
	WebOverridden(platform string) bool
	SetWeb(ctx context.Context, platform string, ws store.WebSyncSettings) error
	ResetWeb(ctx context.Context, platform string) error
}

// Deps are the server's dependencies.
type Deps struct {
	Log *slog.Logger
	// Metrics is this process's request counters; /metrics renders them.
	Metrics   *metrics
	DB        Pinger
	Store     *store.Store
	Poller    Poller
	Platforms []PlatformInfo
	// WebSyncs are the platforms with a browser sync set up, by name.
	WebSyncs map[string]platform.WebSyncer
	Schedule Schedule
	Version  string
	// AllowedHosts are extra hostnames HuntDeck may be reached by (IPs and
	// localhost always work); see guard.go.
	AllowedHosts []string
	// ReadOnly serves a read-only copy, such as a demo (readonly.go).
	ReadOnly bool
	// AIBaseURL and AIModel are the optional model, empty when it is off.
	// Settings reports which, so an off-by-default feature is visible.
	AIBaseURL, AIModel string
	// AI is the configured model, nil when it is off.
	AI *ai.Client
	// PojiaKey and PojiaURL are the disclosed-report library; empty key is off.
	PojiaURL string
	PojiaKey config.Secret
	// ScanURL and ScanKey are a ScopeSentry instance the handoff sends targets to.
	ScanURL string
	ScanKey config.Secret
	// DemoSeed exports seeds for a demo copy (demoseed.go); nil where it can't.
	DemoSeed DemoSeeder
	// Timezone is the IANA zone to show times in until one is chosen in
	// Settings; empty means UTC.
	Timezone string
	// Notify sends test messages; nil where notifications don't run.
	Notify Notifier
	// VNC, if set, is the login browser's display, shown at /novnc/ and
	// in Settings' Browser tab.
	VNC *VNC
	// Browser is the shared login browser; nil where there's none.
	Browser LoginBrowser
	// Accounts edits the settings saved over the environment (Settings >
	// Accounts); nil where they can't be.
	Accounts *overrides.Saved
	// Progress says how far a poll running in this process has got; nil
	// without polls (a read-only copy, tests).
	Progress func(runID int64) (RunProgress, bool)
	// PublicURL is where a read-only copy is published (HUNTDECK_PUBLIC_URL),
	// letting search engines index it; see seo.go.
	PublicURL string
	// XAccount is an X account to follow for news (HUNTDECK_X_ACCOUNT): a
	// read-only copy links to it.
	XAccount string
	// RepoURL is HuntDeck's source repository (HUNTDECK_REPO_URL): a
	// read-only copy links to it, for visitors to run their own.
	RepoURL string
	// FeedbackEmail is where "Send feedback" links mail to; none if empty.
	FeedbackEmail string
	// ClientIPHeader names the header a trusted proxy in front puts the
	// visitor's address in (Cloudflare's CF-Connecting-IP), for a read-only
	// copy's per-visitor limits; "" uses the connection's address.
	ClientIPHeader string
	// Restart restarts the server to apply them; nil where it can't.
	Restart Restarter
	// Password, from HUNTDECK_PASSWORD, guards the whole install behind a
	// login page when set (auth.go). It adds no user accounts: one password,
	// one session cookie. Empty leaves HuntDeck open, as it has always been.
	Password string
	// Language is the interface language when a visitor has no preference
	// of their own: "auto" (the browser's), or a language with a catalog
	// such as "zh" (Deps.Language, from HUNTDECK_LANGUAGE).
	Language string
	// SaveOwn saves the programs added by hand, the whole listing with an
	// edit applied (ownprograms.go); nil where they can't be changed.
	SaveOwn func(ctx context.Context, programs []independent.Program) error
}

type server struct {
	Deps
	// pages holds one template set per language: pages[lang][page].
	pages map[string]map[string]*template.Template
	// messages are the interface's translations (i18n).
	messages *i18n.Bundle
	// auth checks HUNTDECK_PASSWORD and the sessions it hands out; nil when
	// the install is open.
	auth *auth
	// openapi is the API's description, served and shown as the API page.
	openapi *openAPI
	zone    atomic.Pointer[time.Location] // nil until first read; see location
	// ogImage says static/og.png, the link-preview image, is built in.
	ogImage bool
	// ownMu makes saves of programs added by hand take turns.
	ownMu sync.Mutex
	// versions dates the API's answers for Last-Modified (conditional.go).
	versions versionMemo
}

// NewHandler returns the root HTTP handler.
func NewHandler(d Deps) (http.Handler, error) {
	if d.ReadOnly { // never anything of the account's, whatever the caller passed
		d.Accounts, d.Restart, d.Browser, d.VNC, d.DemoSeed, d.WebSyncs = nil, nil, nil, nil, nil, nil
	}
	s := &server{Deps: d}
	messages, err := i18n.Load()
	if err != nil {
		return nil, err
	}
	s.messages = messages
	if d.Password != "" {
		s.auth = newAuth(d.Password)
	}
	if _, err := fs.Stat(staticFS, "static/og.png"); err == nil {
		s.ogImage = true
	}
	assets, err := loadStatic()
	if err != nil {
		return nil, err
	}
	pages, err := loadTemplates(s.shownZone, assets.Version, d.ReadOnly, readOnlyLinks{XAccount: d.XAccount, RepoURL: d.RepoURL},
		s.languages(), s.messages)
	if err != nil {
		return nil, err
	}
	s.pages = pages
	if s.openapi, err = loadOpenAPI(); err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	for _, rt := range s.apiRoutes() {
		mux.HandleFunc(rt.Pattern, rt.Handler)
	}
	mux.Handle("GET /static/", staticHandler(assets))
	mux.HandleFunc("GET /i18n.js", s.i18nScript)
	mux.HandleFunc("GET /robots.txt", s.robots)
	mux.HandleFunc("GET /sitemap.xml", s.sitemap)
	mux.HandleFunc("GET /sitemaps/{file}", s.sitemapFile)
	mux.HandleFunc("GET /llms.txt", s.llms)
	mux.HandleFunc("GET /favicon.ico", rootIcon("favicon.ico"))
	mux.HandleFunc("GET /apple-touch-icon.png", rootIcon("apple-touch-icon.png"))
	mux.HandleFunc("GET /apple-touch-icon-precomposed.png", rootIcon("apple-touch-icon.png"))
	mux.HandleFunc("GET /.well-known/security.txt", s.securityTxt)
	s.routeVNC(mux)
	mux.HandleFunc("GET /api", s.apiDocs)
	mux.HandleFunc("GET /mcp", s.mcpPage)
	mux.HandleFunc("POST /mcp/token", s.mcpToken)
	mux.HandleFunc("POST /mcp/revoke", s.mcpRevoke)
	mux.HandleFunc("/mcp/rpc", s.mcpRPC) // the handler answers 405 + Allow on non-POST itself
	mux.HandleFunc("GET /scan", s.scanPage)
	mux.HandleFunc("POST /scan/save", s.scanSave)
	mux.HandleFunc("POST /scan/now", s.scanNow)
	mux.HandleFunc("GET /pojia", s.pojiaPage)
	mux.HandleFunc("GET /pojia/report", s.pojiaReport)

	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginSubmit)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("POST /settings/language", s.languageSwitch)

	mux.HandleFunc("GET /{$}", s.dashboard)
	mux.HandleFunc("GET /programs", s.programs)
	mux.HandleFunc("GET /programs.csv", s.programsCSV)
	mux.HandleFunc("GET /invites", s.invites)
	mux.HandleFunc("POST /invites/{platform}/{handle}/accept", s.acceptInvite)
	mux.HandleFunc("GET /programs/{platform}/{handle}", s.program)
	mux.HandleFunc("GET /programs/independent/new", s.newOwnProgram)
	mux.HandleFunc("POST /programs/independent/new", s.saveOwnProgram)
	mux.HandleFunc("GET /programs/independent/{handle}/edit", s.editOwnProgram)
	mux.HandleFunc("POST /programs/independent/{handle}/edit", s.saveOwnProgram)
	mux.HandleFunc("POST /programs/independent/{handle}/remove", s.removeOwnProgram)
	mux.HandleFunc("GET /methodology", s.methodology)
	mux.HandleFunc("GET /feedback", s.feedback)
	mux.HandleFunc("GET /stats", s.stats)
	mux.HandleFunc("GET /stats/{platform}", s.statsPlatform)
	mux.HandleFunc("GET /targets", s.targets)
	mux.HandleFunc("GET /queue", s.queuePage)
	mux.HandleFunc("GET /metrics", s.metricsPage)
	mux.HandleFunc("GET /changes", s.changes)
	mux.HandleFunc("GET /changes.atom", s.changesFeed)
	mux.HandleFunc("GET /programs/{platform}/{handle}/changes.atom", s.programChangesFeed)
	mux.HandleFunc("GET /programs/{platform}/{handle}/og.png", s.programOGImage)
	mux.HandleFunc("GET /changes/{id}", s.change)
	mux.HandleFunc("GET /disclosed", s.disclosed)
	mux.HandleFunc("GET /match", s.match)
	mux.HandleFunc("GET /search", s.search)
	mux.HandleFunc("GET /settings", s.settings)
	mux.HandleFunc("POST /settings/poll", s.pollNow)
	mux.HandleFunc("POST /settings/platform", s.saveSchedule)
	mux.HandleFunc("POST /settings/platform/reset", s.resetSchedule)
	mux.HandleFunc("POST /settings/ai/extract", s.extractPolicies)
	mux.HandleFunc("POST /settings/websync", s.saveWebSync)
	mux.HandleFunc("POST /settings/websync/reset", s.resetWebSync)
	mux.HandleFunc("POST /settings/huntscore", s.saveHuntPreset)
	mux.HandleFunc("POST /settings/timezone", s.saveTimezone)
	mux.HandleFunc("POST /settings/accounts", s.saveAccounts)
	mux.HandleFunc("POST /settings/accounts/reset", s.resetAccounts)
	mux.HandleFunc("POST /settings/restart", s.restartNow)
	mux.HandleFunc("GET /settings/restart", s.restartStatus)
	mux.HandleFunc("POST /settings/seed", s.exportDemoSeed)
	mux.HandleFunc("POST /settings/seed/daily", s.setDemoSeedDaily)
	mux.HandleFunc("POST /settings/browser/show", s.browserShow)
	mux.HandleFunc("POST /settings/browser/close", s.browserClose)
	mux.HandleFunc("GET /settings/notifications", s.notifications)
	// The nav says "Notifications"; people type /notifications. It used to 404.
	mux.HandleFunc("GET /notifications", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/settings/notifications", http.StatusFound)
	})
	mux.HandleFunc("GET /settings/notifications/new", s.newChannel)
	mux.HandleFunc("GET /settings/notifications/preview", s.channelPreview)
	mux.HandleFunc("GET /settings/notifications/{id}", s.editChannel)
	mux.HandleFunc("POST /settings/notifications", s.saveChannel)
	mux.HandleFunc("POST /settings/notifications/address", s.saveBaseURL)
	mux.HandleFunc("POST /settings/notifications/test", s.testChannel)
	mux.HandleFunc("POST /settings/notifications/{id}", s.saveChannel)
	mux.HandleFunc("POST /settings/notifications/{id}/delete", s.deleteChannel)
	mux.HandleFunc("POST /settings/notifications/{id}/test", s.testChannel)
	mux.HandleFunc("POST /programs/{platform}/{handle}/ignore", s.setIgnored)
	mux.HandleFunc("POST /programs/{platform}/{handle}/note", s.setProgramNote)
	mux.HandleFunc("POST /programs/ignore", s.ignoreMany)
	mux.HandleFunc("POST /programs/{platform}/{handle}/tags", s.setTags)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONError(w, http.StatusNotFound, "no such endpoint")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.renderError(w, r, http.StatusNotFound, "Page not found.")
	})

	// CrossOriginProtection rejects cross-site POSTs (CSRF) using the
	// browser's Sec-Fetch-Site/Origin headers. curl and scripts send
	// neither, so they pass.
	csrf := http.NewCrossOriginProtection()
	inner := http.Handler(s.readOnly(mux))
	if s.Metrics != nil {
		inner = s.Metrics.middleware(s.Log, inner)
	}
	if s.auth != nil { // the access password, when one is set (auth.go)
		// A bearer token is another key to the same gate; it is checked first so a
		// script never needs a cookie jar.
		inner = s.tokenGate(inner, s.auth.middleware(inner))
	}
	// A panic becomes a logged 500 rather than a dropped connection; the
	// recoverer sits inside the security headers so its error page carries them.
	return securityHeaders(s.indexable(), s.recoverer(compress(hostGuard(d.AllowedHosts, csrf.Handler(inner))))), nil
}

// apiRoute is one of the API's endpoints.
type apiRoute struct {
	Pattern string // as the mux and openapi.json have it: "GET /api/v1/programs"
	Handler http.HandlerFunc
}

// apiRoutes are the API's endpoints, in the order the API page lists them.
// openapi.json documents each of them and nothing else (a test checks).
func (s *server) apiRoutes() []apiRoute {
	return []apiRoute{
		{"GET /api/v1/board", s.apiBoard},
		{"GET /api/v1/queue", s.apiQueue},
		{"GET /api/v1/programs", s.conditional(s.apiPrograms)},
		{"GET /api/v2/programs", s.conditional(s.apiProgramsV2)},
		{"GET /api/v1/invites", s.conditional(s.apiInvites)},
		{"GET /api/v2/invites", s.conditional(s.apiInvitesV2)},
		{"POST /api/v1/invites/{platform}/{handle}/accept", s.apiAcceptInvite},
		{"GET /api/v1/programs/{platform}/{handle}", s.conditional(s.apiProgram)},
		{"GET /api/v2/programs/{platform}/{handle}", s.conditional(s.apiProgramV2)},
		{"GET /api/v1/programs/{platform}/{handle}/scope.burp.json", s.apiScopeExport},
		{"GET /api/v1/programs/{platform}/{handle}/scope.zap.context", s.apiScopeExport},
		{"GET /api/v1/programs/{platform}/{handle}/scope.caido.json", s.apiScopeExport},
		{"GET /api/v1/targets", s.apiTargets},
		{"GET /api/v1/match", s.conditional(s.apiMatch)},
		{"POST /api/v1/match", s.apiMatchMany},
		// Tokens are the one pair that is not /api/vN: they manage the gate itself.
		{"POST /api/tokens", s.createAPIToken},
		{"POST /api/tokens/revoke", s.revokeAPIToken},
		{"POST /api/tokens/rotate", s.rotateAPIToken},
		{"POST /api/tokens/delete", s.deleteAPIToken},
		{"GET /api/v1/changes", s.apiChanges},
		{"GET /api/v1/disclosed", s.apiDisclosed},
		{"GET /api/v1/stats", s.apiStats},
		{"GET /api/v1/stats/platforms", s.apiStatsPlatforms},
		{"GET /api/v1/stats/platforms/{platform}", s.apiStatsPlatform},
		{"GET /api/v1/status", s.apiStatus},
		{"POST /api/v1/poll/{platform}", s.apiPoll},
		{"GET /api/v1/openapi.json", s.openapi.serveSpec},
		{"GET /healthz", s.healthz},
	}
}

// NewServer wraps h in an http.Server with conservative timeouts.
func NewServer(h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}

// cspNonce is a fresh nonce for an answer's script-src. HuntDeck's own
// pages have no inline scripts and never use it: it's for Cloudflare in
// front of a copy, whose bot detection (JavaScript detections, always on
// with Bot Fight Mode) injects an inline script into each page and gives
// it the nonce it finds in this header, so it runs under a policy that
// still refuses any other inline script. A page Cloudflare caches keeps
// its nonce for as long (5 minutes on a read-only copy); markup can't get
// in for it to matter, as templates escape everything.
func cspNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // never fails (crypto/rand)
	return base64.StdEncoding.EncodeToString(b)
}

// imagePolicy is the Content-Security-Policy of a raster image (PNG, ICO):
// opened on its own, a browser shows it in a page of its own making, whose
// inline styles the pages' policy would refuse (console errors). An image
// runs nothing, so it may have them; anything else stays refused. The
// handlers serving images set it over securityHeaders' policy.
const imagePolicy = "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; frame-ancestors 'none'; sandbox"

// rasterImage says whether path names a PNG or ICO file.
func rasterImage(path string) bool {
	return strings.HasSuffix(path, ".png") || strings.HasSuffix(path, ".ico")
}

// securityHeaders sets the headers every answer gets. Search engines are
// asked to leave out every page unless indexable (seo.go), and then still
// the API, searches and scope checks.
func securityHeaders(indexable bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self' 'nonce-"+cspNonce()+"'; "+
			"form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		// Program links go to platforms; don't tell them where HuntDeck lives.
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if !indexable || noindexPath(r.URL.Path) {
			h.Set("X-Robots-Tag", "noindex, nofollow") // a personal tool, or an unpublished copy: never in search results
		}
		next.ServeHTTP(w, r)
	})
}

// healthz is unauthenticated, so it reports only ok/unavailable and never
// the underlying error.
func (s *server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.DB.Ping(ctx); err != nil {
		s.Log.Warn("health check failed", "err", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unavailable"}` + "\n"))
		return
	}
	_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
}

// pageData is what every page template receives.
type pageData struct {
	Title string
	Nav   string
	// Feed is the page's Atom feed (feeds.go), linked in its head for
	// feed readers; FeedTitle names it.
	Feed, FeedTitle string
	Tour            string // a read-only copy's note on a page it shows empty (demoTour)
	Feedback        string // the feedback page's link from this page; "" without Deps.FeedbackEmail
	FeedbackOut     bool   // Feedback opens a new issue on the source repository (a read-only copy with Deps.RepoURL)
	// A published read-only copy's (seo.go): whether search engines may
	// index the page, its canonical address, its description, and the
	// link-preview image's address (a path is the page's own, made
	// absolute) and what it shows.
	Index                                   bool
	Canonical, Description, Image, ImageAlt string
	// SearchTitle is the title in search results and link previews
	// (pageTitles; Title if ""), Crumbs the page's trail back for them,
	// and JSONLD what structuredData makes of it.
	SearchTitle string
	Crumbs      []crumb
	JSONLD      template.JS
	Version     string
	Flash       string
	FlashErr    string
	Updated     time.Time // when the latest successful poll finished, for the top bar
	Data        any
	// Lang is the page's language ("zh"), Langs the languages a visitor can
	// switch to (the sidebar's switcher), Current marking the page's own.
	Lang  string
	Langs []langLink
	// Auth says the install asks for an access password (auth.go), so pages
	// offer a way to sign out; Path is the page's own address, to come back
	// to after switching language or signing in.
	Auth bool
	Path string
}

func (s *server) render(w http.ResponseWriter, r *http.Request, status int, page string, pd pageData) {
	lang := s.langOf(r)
	t, ok := s.pages[lang][page]
	if !ok {
		// A page missing from a language's set falls back to English, which
		// has every page: a translator can land pages one at a time.
		lang = i18n.English
		t, ok = s.pages[lang][page]
	}
	if !ok {
		s.Log.Error("missing template", "page", page)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	pd.Lang = lang
	// Page titles are messages too ("Hunt board", "Settings"), but only the
	// fixed ones: a page named after a program keeps the program's name.
	if pageTitleKeys[pd.Title] {
		pd.Title = s.messages.T(lang, pd.Title)
	}
	pd.Langs = s.langLinks(lang)
	pd.Auth = s.auth != nil
	pd.Path = r.URL.Path // the page itself: a language switch comes back here, without another page's filters
	pd.Version = s.Version
	if s.ReadOnly {
		pd.Tour = demoTour(pd.Nav, r.URL.Query().Get("visibility"))
	}
	pd.Feedback = feedbackPage(s.FeedbackEmail, r.URL.RequestURI())
	if s.ReadOnly && s.RepoURL != "" {
		// Feedback goes where the code is: a new issue, not an email.
		pd.Feedback, pd.FeedbackOut = s.RepoURL+"/issues/new", true
	}
	if s.indexable() {
		pd.Index = !noindexPath(r.URL.Path)
		pd.Canonical = s.PublicURL + r.URL.EscapedPath()
		if pd.Description == "" {
			pd.Description = pageDescriptions[pd.Nav]
		}
		if pd.Description == "" {
			pd.Description = siteDescription
		}
		if strings.HasPrefix(pd.Image, "/") { // the page's own (a program's card)
			pd.Image = s.PublicURL + pd.Image
		} else if s.ogImage {
			pd.Image, pd.ImageAlt = s.PublicURL+"/static/og.png", siteImageAlt
		}
		if pd.SearchTitle == "" {
			pd.SearchTitle = pageTitles[r.URL.Path]
		}
		if pd.SearchTitle == "" {
			pd.SearchTitle = pd.Title + " · HuntDeck"
		}
		pd.JSONLD = s.structuredData(r.URL.Path, pd.Crumbs)
	} else {
		pd.SearchTitle = "" // other installs keep the short title
	}
	if at, err := s.Store.LastUpdate(r.Context()); err != nil {
		s.Log.Error("last update", "err", err)
	} else if at != nil {
		pd.Updated = *at
	}
	s.location(r.Context()) // the template's time funcs read it
	if pd.Flash == "" {
		pd.Flash = flashMessages[r.URL.Query().Get("msg")]
	}
	if pd.FlashErr == "" {
		pd.FlashErr = flashErrors[r.URL.Query().Get("err")]
	}
	var buf bytes.Buffer
	root := "layout.html"
	if page == "login" { // a page of its own, without the shell
		root = "login.html"
	}
	if err := t.ExecuteTemplate(&buf, root, pd); err != nil {
		s.Log.Error("render page", "page", page, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// fragment renders one of a page's templates on its own, for a script to
// swap into the page.
func (s *server) fragment(w http.ResponseWriter, r *http.Request, page, name string, data any) {
	t, ok := s.pages[s.langOf(r)][page]
	if !ok {
		t, ok = s.pages[i18n.English][page]
	}
	if !ok {
		s.Log.Error("missing template", "page", page)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.location(r.Context()) // the template's time funcs read it
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		s.Log.Error("render fragment", "page", page, "name", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = buf.WriteTo(w)
}

func (s *server) renderError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	s.render(w, r, status, "error", pageData{Title: http.StatusText(status), Data: msg})
}

func (s *server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("request failed", "path", r.URL.Path, "err", err)
	s.renderError(w, r, http.StatusInternalServerError, "Something went wrong. The details are in the server log.")
}

// Flash messages are looked up by fixed keys so a URL can't inject text.
var flashMessages = map[string]string{
	"program-saved":      "Saved.",
	"program-removed":    "Program removed. Its history stays: choose Status › Removed to see it.",
	"program-ignored":    "Ignored. It leaves lists, exports, picks and alerts; its page stays.",
	"program-unignored":  "Back in your lists and picks.",
	"poll-started":       "Poll started. Refresh in a minute to see the results.",
	"settings-saved":     "Saved. The new schedule applies now; no restart needed.",
	"settings-reset":     "Back to the defaults from your environment.",
	"websync-saved":      "Browser sync saved. The new schedule applies now.",
	"websync-reset":      "Browser sync back to the default: on, once a day.",
	"seed-exported":      "Demo seed exported to the data directory.",
	"seed-daily-on":      "A demo seed will be exported every 12 hours.",
	"seed-daily-off":     "Regular demo seed export turned off.",
	"ai-off":             "No model is configured: set HUNTDECK_AI_BASE_URL and HUNTDECK_AI_MODEL first.",
	"ai-read":            "The model read the policies it had not seen; the program pages show what it found.",
	"ai-none":            "Every policy here has been read already; nothing was sent.",
	"ai-failed":          "The endpoint refused the request; the server log has the reason.",
	"huntscore-saved":    "HuntScore weighting saved and every program rescored.",
	"timezone-saved":     "Time zone saved. Dates and times now show in it.",
	"channel-saved":      "Channel saved.",
	"channel-deleted":    "Channel deleted.",
	"test-sent":          "Test message sent. Check the channel.",
	"address-saved":      "Address saved. Messages link here from now on.",
	"invite-accepted":    "Invitation accepted. HuntDeck is refreshing that program now: its scope and stats show in a minute or two.",
	"accounts-saved":     "Saved, encrypted. HuntDeck restarts to use it, and a platform with new credentials is polled straight after, so its sign-in shows.",
	"accounts-reset":     "Back to the values in your environment (.env). HuntDeck restarts to use them.",
	"accounts-unchanged": "Nothing changed, so nothing was saved.",
	"invite-accepting":   "Accepting: the platform is slow to answer (it may be signing in). Refresh in a minute; the program refreshes once it's accepted.",
}

var flashErrors = map[string]string{ //nolint:gosec // flash message keys, not credentials
	"poll-running":    "A poll of that platform is already running.",
	"seed-failed":     "The demo seed couldn't be exported or saved; the log says why.",
	"poll-unknown":    "That platform isn't configured.",
	"poll-disabled":   "Polling isn't available in this process.",
	"poll-off":        "That platform is disabled. Enable it below to poll it.",
	"invite-none":     "That program has no pending invitation (already accepted?).",
	"invite-cannot":   "That platform's invitations can't be accepted from HuntDeck yet: accept it on the platform.",
	"invite-off":      "That platform is disabled in Settings: enable it to accept its invitations.",
	"invite-failed":   "The platform didn't accept the invitation. The details are in the server log.",
	"browser-none":    "There's no login browser: no platform logs in to its website.",
	"browser-unknown": "That platform doesn't log in with the browser.",
	"browser-failed":  "The browser didn't open. The details are in the server log.",
	"browser-busy":    "A login is running in the browser just now: close it once that's done.",
}
