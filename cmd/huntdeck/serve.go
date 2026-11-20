package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/ai"

	"github.com/mdpsec/huntdeck/internal/browser"
	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/huntscore"
	"github.com/mdpsec/huntdeck/internal/logging"
	"github.com/mdpsec/huntdeck/internal/notify"
	"github.com/mdpsec/huntdeck/internal/overrides"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scanhandoff"
	"github.com/mdpsec/huntdeck/internal/scheduler"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/scopesentry"
	"github.com/mdpsec/huntdeck/internal/store"
	"github.com/mdpsec/huntdeck/internal/syncer"
	"github.com/mdpsec/huntdeck/internal/version"
	"github.com/mdpsec/huntdeck/internal/web"
)

const shutdownTimeout = 10 * time.Second

// vncPort is the login display's VNC server, on the loopback interface
// (inside the container: only HuntDeck's noVNC proxy reaches it).
const vncPort = 5900

// app is the shared setup every database-backed command needs.
type app struct {
	cfg    config.Config
	log    *slog.Logger
	db     *db.DB
	store  *store.Store
	syncer *syncer.Syncer
	saved  *overrides.Saved // what Settings > Accounts saved over the environment
}

// zone is the time zone to print times in: the one chosen in Settings,
// else HUNTDECK_TIMEZONE.
func (a *app) zone(ctx context.Context) *time.Location {
	loc, err := a.store.Timezone(ctx, a.cfg.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

func (a *app) close() {
	if err := a.db.Close(); err != nil {
		a.log.Error("closing database", "err", err)
	}
}

// openApp loads config, builds the logger, opens the database and applies
// migrations. quiet raises the log level to warn so read-only commands keep
// stderr clean. Failures are reported before returning ok=false.
func openApp(ctx context.Context, stderr io.Writer, quiet bool) (*app, bool) {
	cfg, err := config.Load(config.OSSource)
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck: invalid configuration:\n%v\n", err)
		return nil, false
	}
	level := cfg.LogLevel
	if quiet {
		level = max(level, slog.LevelWarn)
	}
	log := logging.New(stderr, level, cfg.LogFormat)

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		log.Error("cannot create data directory", "path", cfg.DataDir, "err", err)
		return nil, false
	}
	database, err := db.Open(ctx, cfg.DBPath())
	if err != nil {
		log.Error("cannot open database", "err", err)
		return nil, false
	}
	a := &app{cfg: cfg, log: log, db: database}
	migs, err := db.Migrations()
	if err != nil {
		log.Error("invalid embedded migrations", "err", err)
		a.close()
		return nil, false
	}
	n, err := database.Migrate(ctx, migs, cfg.BackupDir(), log)
	if err != nil {
		log.Error("database migration failed", "err", err)
		a.close()
		return nil, false
	}
	log.Info("database ready", "path", cfg.DBPath(), "schema_version", len(migs), "migrations_applied", n)
	a.store = store.New(database, nil)
	a.syncer = syncer.New(a.store, log, 4)
	seedURL := cfg.SeedURL
	if cfg.ReadOnly { // a demo loads its own seed
		seedURL = ""
	}
	if err := autoSeed(ctx, a.store, cfg.DataDir, seedURL, nil, log); err != nil {
		log.Error("cannot load seed", "err", err)
		a.close()
		return nil, false
	}
	if cfg.ReadOnly { // shown to other people: only a signed-out seed's data
		if err := a.store.CheckSignedOut(ctx); err != nil {
			log.Error("read-only refuses this database", "err", err)
			a.close()
			return nil, false
		}
	}
	a.saved = overrides.New(a.store, cfg.DataDir, config.OSSource)
	if !cfg.ReadOnly { // a read-only copy uses no saved credentials
		a.applySaved(ctx)
	}
	// Targets stored by an older parser are derived again, then rescored.
	if n, err := a.store.Retarget(ctx, scope.Version, scope.Targets); err != nil {
		log.Error("cannot update scope targets", "err", err)
		a.close()
		return nil, false
	} else if n > 0 {
		log.Info("scope targets derived again", "entries", n, "scope_version", scope.Version)
		if err := huntscore.Refresh(ctx, a.store, a.store.Now()); err != nil {
			log.Warn("cannot rescore after deriving targets", "err", err)
		}
		if err := a.store.RefreshHuntQueue(ctx, a.store.Now()); err != nil {
			a.log.Warn("hunt queue not rebuilt", "err", err)
		}
	}
	// The API's per-field updated_at: dated from history the first time,
	// and a seed's or an upgrade's values brought up to date.
	if err := a.store.TrackValues(ctx); err != nil {
		log.Warn("value times not updated", "err", err)
	}
	return a, true
}

// applySaved puts the settings saved in Settings > Accounts over the
// environment's. Ones that can't be used are logged (by name, never their
// values) and left out, so HuntDeck still starts and Settings can fix them.
func (a *app) applySaved(ctx context.Context) {
	src, unreadable, err := a.saved.Source(ctx)
	if err != nil {
		a.log.Error("settings saved in Settings > Accounts can't be read; using the environment's", "err", err)
		return
	}
	if len(unreadable) > 0 {
		a.log.Warn("settings saved in Settings > Accounts can't be decrypted (the key file was lost or replaced); using the environment's",
			"settings", strings.Join(unreadable, ","))
	}
	cfg, err := config.Load(src)
	if err != nil {
		a.log.Error("settings saved in Settings > Accounts aren't used: with them the configuration is invalid", "err", err)
		return
	}
	a.cfg = cfg
}

// serve runs the server until it's stopped, starting it again after a
// restart that applies settings saved in Settings.
func serve(_, stderr io.Writer) int {
	ctx, stop := signalContext()
	defer stop()
	for {
		code, again := serveOnce(ctx, stderr)
		if !again || ctx.Err() != nil {
			return code
		}
	}
}

// serveOnce runs the server until ctx ends or a restart is asked for
// (again: true).
func serveOnce(parent context.Context, stderr io.Writer) (code int, again bool) {
	ctx, restartNow := context.WithCancel(parent)
	defer restartNow()

	a, ok := openApp(ctx, stderr, false)
	if !ok {
		return 1, false
	}
	defer a.close()
	log := a.log
	// Hold the data directory for as long as this server runs: a restore
	// elsewhere has to know it cannot swap the database underneath us.
	if release, err := lockData(a.cfg.DataDir); err != nil {
		log.Error("data directory is in use", "err", err)
		return 1, false
	} else {
		defer release()
	}
	restart := newRestarter(ctx, restartNow, log, a.store)

	// With VNC on, login browsers show on one display that noVNC (in the
	// web UI) can watch; without it, or if it won't start, each login has
	// a display of its own.
	var vnc *web.VNC
	display := ""
	if a.cfg.VNC {
		shared, err := browser.StartShared(ctx, vncPort)
		if err != nil {
			log.Warn("login browser display for VNC not started", "err", err)
		} else {
			defer shared.Close()
			display = shared.Display
			vnc = &web.VNC{Addr: shared.VNCAddr, Dir: a.cfg.NoVNCDir}
			log.Info("login browser display ready to watch", "path", "/novnc/")
		}
	}
	host := loginBrowser(a.cfg, display, log)
	defer func() { _ = host.Close() }() // closed after the polls below, which may be using it
	platforms, err := buildPlatforms(a.cfg, host)
	if err != nil {
		log.Error("cannot configure platforms", "err", err)
		return 1, false
	}
	if len(platforms) == 0 {
		log.Warn("no platform credentials configured; nothing will be polled (see .env.example)")
	}

	sched, err := openSchedule(ctx, a.store, platforms)
	if err != nil {
		log.Error("cannot load polling settings", "err", err)
		return 1, false
	}
	poller := &backgroundPoller{ctx: ctx, log: log, store: a.store, syncer: a.syncer, sched: sched, platforms: platforms}
	var infos []web.PlatformInfo
	webSyncs := map[string]platform.WebSyncer{}
	for _, p := range platforms {
		_, discloses := p.Platform.(platform.DisclosureFetcher)
		info := web.PlatformInfo{Name: p.Name(), AcceptsInvites: syncer.CanAcceptInvites(p.Platform), Discloses: discloses}
		if cr, ok := p.Platform.(platform.CredentialReporter); ok {
			info.Credentials = cr.Credentials
		}
		infos = append(infos, info)
		if ws, ok := p.Platform.(platform.WebSyncer); ok && ws.WebSyncConfigured() {
			webSyncs[p.Name()] = ws
		}
	}
	if a.cfg.ReadOnly {
		if infos, err = heldPlatforms(ctx, a.store); err != nil {
			log.Error("cannot list the seed's platforms", "err", err)
			return 1, false
		}
	}
	notifier := notify.New(a.store, log, a.cfg.Timezone)
	progress := func(id int64) (web.RunProgress, bool) { // Settings shows a running poll moving
		p, ok := a.syncer.RunProgress(id)
		return web.RunProgress(p), ok
	}
	deps := web.Deps{
		Log:     log,
		Metrics: web.NewMetrics(), DB: a.db, Store: a.store, Poller: poller, Progress: progress, Platforms: infos, WebSyncs: webSyncs, Schedule: sched,
		Version: version.String(), AllowedHosts: a.cfg.AllowedHosts, Timezone: a.cfg.Timezone, Notify: notifier, VNC: vnc,
		Language: a.cfg.Language, Password: a.cfg.Password.Reveal(),
		Browser: host, Accounts: a.saved, Restart: restart, ReadOnly: a.cfg.ReadOnly, AIBaseURL: a.cfg.AIBaseURL, AIModel: a.cfg.AIModel, AI: aiClient(a.cfg), PojiaURL: a.cfg.PojiaURL, PojiaKey: a.cfg.PojiaKey, ScanURL: a.cfg.ScanURL, ScanKey: a.cfg.ScanKey, JevURL: a.cfg.JevURL, JevKey: a.cfg.JevKey, JevModel: a.cfg.JevModel, JevProxy: a.cfg.JevProxy, JevProvider: a.cfg.JevProvider, JevAccountID: a.cfg.JevAccountID, JevWorkspaceID: a.cfg.JevWorkspaceID, FeedbackEmail: a.cfg.FeedbackEmail, ClientIPHeader: a.cfg.ClientIPHeader, PublicURL: a.cfg.PublicURL, XAccount: a.cfg.XAccount, RepoURL: a.cfg.RepoURL,
	}
	seeds := &demoSeeds{st: a.store, dir: a.cfg.DataDir, log: log}
	if !a.cfg.ReadOnly {
		deps.DemoSeed, deps.SaveOwn = seeds, a.syncer.SaveOwn
	}
	if a.cfg.ReadOnly {
		deps.Accounts, deps.Restart, deps.Browser = nil, nil, nil
		log.Info("read-only: nothing can be changed, Settings and invites aren't served, nothing is polled")
	}
	handler, err := web.NewHandler(deps)
	if err != nil {
		log.Error("cannot build web handler", "err", err)
		return 1, false
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", a.cfg.Listen)
	if err != nil {
		log.Error("cannot listen", "addr", a.cfg.Listen, "err", err)
		return 1, false
	}
	srv := web.NewServer(handler, log)

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Info("huntdeck started", "version", version.String(), "addr", ln.Addr().String(), "data_dir", a.cfg.DataDir)

	// Platforms whose credentials were just saved are polled now, so
	// Settings shows whether they work.
	if names, err := a.saved.TakePollAfterRestart(ctx); err != nil {
		log.Warn("cannot read which platforms to poll after the restart", "err", err)
	} else {
		for _, name := range names {
			if err := poller.PollNow(name, syncer.ModeQuick); err != nil {
				log.Info("platform not polled after saving its settings", "platform", name, "err", err)
			}
		}
	}

	var wg sync.WaitGroup
	if !a.cfg.ReadOnly {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seeds.Run(ctx)
		}()
	}
	wg.Add(4)
	go func() {
		defer wg.Done()
		scheduler.Run(ctx, log, a.store, a.syncer, sched, scheduleJobs(platforms))
	}()
	go func() {
		defer wg.Done()
		if !a.cfg.ReadOnly { // a read-only copy sends nothing anywhere
			notifier.Run(ctx)
		}
	}()
	go func() {
		defer wg.Done()
		// HuntQueue is derived data: rebuild it now and then, so the table follows
		// the polls without anyone running a command.
		if a.cfg.ReadOnly {
			return
		}
		tick := time.NewTicker(30 * time.Minute)
		defer tick.Stop()
		for {
			// One HuntScore row per day: the backtest in docs/design/ai-layer.md
			// counts them as its point-in-time cuts, so they have to keep coming
			// without anyone running a command.
			now := a.store.Now()
			// The day column is UTC (huntscore writes time.DateOnly on UTC), so the
			// comparison has to be too: at 02:00 in Shanghai it is still yesterday
			// in UTC, and a local-date check would ask for a row that exists.
			if day, err := a.store.LatestScoreDay(ctx); err == nil && scoreDayDue(day, now) {
				switch err := huntscore.Refresh(ctx, a.store, now); {
				case err != nil && ctx.Err() == nil:
					log.Warn("daily score refresh failed", "err", err)
				case err == nil:
					// One line a day, so "did today's cut land?" is answerable from
					// the logs without opening the database. Name the day that was
					// written, not the one that was already there.
					log.Info("daily score snapshot written", "day", now.UTC().Format(time.DateOnly))
				}
			}
			if err := a.store.RefreshHuntQueue(ctx, a.store.Now()); err != nil && ctx.Err() == nil {
				log.Warn("hunt queue not rebuilt", "err", err)
			}
			// The same clock hands new targets to the scanner, when the operator
			// turned that on: off is the default and the honest one.
			if a.cfg.ScanKey.Reveal() != "" {
				client := &scopesentry.Client{BaseURL: a.cfg.ScanURL, Key: a.cfg.ScanKey.Reveal()}
				if n, err := scanhandoff.Run(ctx, a.store, client, false); err != nil && ctx.Err() == nil {
					log.Warn("scan handoff failed", "err", err)
				} else if n > 0 {
					log.Info("scan tasks created", "tasks", n)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()

	go func() {
		defer wg.Done()
		web.Warm(ctx, deps)
	}()

	exit := 0
	select {
	case err := <-errc:
		log.Error("http server stopped", "err", err)
		exit = 1
		restartNow() // stops the rest; not a restart, as exit says
	case <-ctx.Done():
	}

	again = parent.Err() == nil && restart.requested()
	if again {
		log.Info("restarting")
	} else {
		log.Info("shutting down")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
		exit = 1
	}
	if exit == 0 {
		if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server error", "err", err)
			exit = 1
		}
	}
	wg.Wait() // in-flight polls record their outcome before the DB closes
	poller.wg.Wait()
	if err := host.Close(); err != nil {
		log.Warn("login browser not closed", "err", err)
	}
	return exit, again && exit == 0
}

func migrate(_, stderr io.Writer) int {
	ctx, stop := signalContext()
	defer stop()
	a, ok := openApp(ctx, stderr, false)
	if !ok {
		return 1
	}
	a.close()
	return 0
}

// scoreDayDue reports whether the day's HuntScore row still has to be written.
// The day column is a UTC date, so the comparison is too: at 02:00 in Shanghai
// it is still yesterday in UTC, and a local-date check would ask for a row that
// already exists every evening east of Greenwich.
func scoreDayDue(latest string, now time.Time) bool {
	return latest != now.UTC().Format(time.DateOnly)
}

// aiClient is the configured model, or nil when it is off: one place that
// decides what configured means, for the server and the settings page.
func aiClient(cfg config.Config) *ai.Client {
	if strings.TrimSpace(cfg.AIBaseURL) == "" || strings.TrimSpace(cfg.AIModel) == "" {
		return nil
	}
	return &ai.Client{BaseURL: cfg.AIBaseURL, APIKey: cfg.AIKey.Reveal(), Model: cfg.AIModel}
}
