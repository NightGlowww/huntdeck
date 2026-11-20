package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/jev"
	"github.com/mdpsec/huntdeck/internal/overrides"
	"github.com/mdpsec/huntdeck/internal/scanhandoff"
	"github.com/mdpsec/huntdeck/internal/scopesentry"

	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/store"
)

// doctorCmd looks the data directory over and says what needs attention: it is
// the check to run first when something is off, before reading logs.
func doctorCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	data := fs.String("data", "", "data directory (default $HUNTDECK_DATA_DIR, else ./data)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	dir := *data
	if dir == "" {
		dir = os.Getenv("HUNTDECK_DATA_DIR")
	}
	if dir == "" {
		dir = "data"
	}
	fmt.Fprintf(stdout, "huntdeck doctor: %s\n\n", dir)

	fails := 0
	ok := func(format string, a ...any) { fmt.Fprintf(stdout, "  ok    "+format+"\n", a...) }
	warnings := 0
	warn := func(format string, a ...any) {
		warnings++
		fmt.Fprintf(stdout, "  warn  "+format+"\n", a...)
	}
	bad := func(format string, a ...any) {
		fails++
		fmt.Fprintf(stdout, "  FAIL  "+format+"\n", a...)
	}

	// The directory itself: present, writable, and not world-readable.
	//nolint:gosec // the directory is the operator's --data or the environment's
	info, err := os.Stat(dir)
	switch {
	case os.IsNotExist(err):
		bad("there is no %s (HUNTDECK_DATA_DIR)", dir)
	case err != nil:
		bad("stat %s: %v", dir, err)
	default:
		if !info.IsDir() {
			bad("%s is not a directory", dir)
			break
		}
		if dir != os.TempDir() && info.Mode().Perm()&0o007 != 0 {
			warn("%s is readable by others (%s)", dir, info.Mode().Perm())
		}
		if tmp, err := os.CreateTemp(dir, ".doctor-*"); err != nil {
			bad("%s is not writable: %v", dir, err)
		} else {
			_ = tmp.Close()
			//nolint:gosec // the file was just created by this check, in the data directory
			_ = os.Remove(tmp.Name())
			ok("%s is writable", dir)
		}
	}

	doctorProxy(context.Background(), ok, bad)
	doctorAI(context.Background(), filepath.Join(dir, "huntdeck.db"), ok, warn)

	// The database: presence, schema version against this binary's migrations.
	dbPath := filepath.Join(dir, "huntdeck.db")
	//nolint:gosec // the path is the operator's data directory plus a constant name
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		warn("no huntdeck.db yet: the next start creates and migrates one")
	} else if err == nil {
		doctorDatabase(context.Background(), dbPath, ok, warn, bad)
		// Freshness: the database can be sound and the instance still asleep.
		doctorFreshness(context.Background(), dbPath, ok, warn, bad)
	} else {
		bad("stat the database: %v", err)
	}

	// The key that decrypts credentials saved in Settings.
	keyPath := filepath.Join(dir, "secret.key")
	//nolint:gosec // the data directory's own key file
	if info, err := os.Stat(keyPath); err == nil {
		if info.Mode().Perm()&0o077 != 0 {
			warn("secret.key is readable by others (%s); chmod 600 it", info.Mode().Perm())
		} else {
			ok("secret.key is present and private")
		}
	} else {
		warn("no secret.key: credentials saved in Settings (if any) won't decrypt")
	}

	// Platform logins kept between restarts.
	if entries, err := os.ReadDir(filepath.Join(dir, "sessions")); err == nil {
		ok("sessions for %d platform(s)", len(entries))
	} else {
		ok("no platform sessions yet")
	}

	// Backups: the pre-migration copies the app takes, and the archives a
	// schedule writes, which are only worth having if they are fresh.
	backupDir := filepath.Join(dir, "backups")
	if entries, err := os.ReadDir(backupDir); err == nil {
		var migrations, archives []string
		var newestMigration, newestArchive time.Time
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			fi, err := e.Info()
			if err != nil {
				continue
			}
			switch {
			case strings.HasSuffix(e.Name(), ".db"):
				migrations = append(migrations, e.Name())
				if fi.ModTime().After(newestMigration) {
					newestMigration = fi.ModTime()
				}
			case strings.HasPrefix(e.Name(), "huntdeck-backup-") && strings.HasSuffix(e.Name(), ".tar.gz"):
				archives = append(archives, e.Name())
				if fi.ModTime().After(newestArchive) {
					newestArchive = fi.ModTime()
				}
			}
		}
		sort.Strings(migrations)
		if len(migrations) == 0 {
			ok("no pre-migration backups yet")
		} else {
			ok("%d pre-migration backup(s), newest %s", len(migrations), newestMigration.UTC().Format(time.RFC3339))
		}
		switch {
		case len(archives) == 0:
			warn("no scheduled backups: see docs/deploy.md, Automatic backups")
		case time.Since(newestArchive) > 48*time.Hour:
			warn("the newest backup archive is %s old (%d kept): is the schedule running?",
				humanBytes(int64(time.Since(newestArchive)/time.Hour)), len(archives))
		default:
			ok("%d backup archive(s), newest %s ago", len(archives), humanDuration(time.Since(newestArchive)))
		}
	}

	// Free space: a full volume is the failure that looks like everything else.
	if free, total, err := diskFree(dir); err == nil {
		pct := float64(free) / float64(total) * 100
		switch {
		case free < 256<<20:
			bad("only %s free of %s (%.0f%%)", humanBytes(free), humanBytes(total), pct)
		case free < 2<<30:
			warn("%s free of %s (%.0f%%)", humanBytes(free), humanBytes(total), pct)
		default:
			ok("%s free of %s (%.0f%%)", humanBytes(free), humanBytes(total), pct)
		}
	}

	fmt.Fprintln(stdout)
	if fails > 0 {
		fmt.Fprintf(stdout, "%d check(s) failed\n", fails)
		return 1
	}
	// "No problems found" next to a page of warnings reads as a lie: say which
	// it is, so the last line agrees with the ones above it.
	if warnings > 0 {
		fmt.Fprintf(stdout, "no failures, %d warning(s)\n", warnings)
	} else {
		fmt.Fprintln(stdout, "no problems found")
	}
	return 0
}

// doctorDatabase reports the schema version against this binary's migrations.
func doctorDatabase(ctx context.Context, path string, ok, warn, bad func(string, ...any)) {
	d, err := db.Open(ctx, path)
	if err != nil {
		bad("open the database: %v", err)
		return
	}
	defer func() { _ = d.Close() }()
	migs, err := db.Migrations()
	if err != nil {
		bad("read the migrations: %v", err)
		return
	}
	have, err := schemaVersion(ctx, d)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			warn("the database has no schema yet: the next start migrates it")
			return
		}
		bad("read the schema version: %v", err)
		return
	}
	switch {
	case have == int64(len(migs)):
		ok("database schema v%d, up to date", have)
	case have < int64(len(migs)):
		warn("database schema v%d, this binary has v%d: the next start migrates it", have, len(migs))
	default:
		bad("database schema v%d is newer than this binary (v%d): use a newer HuntDeck", have, len(migs))
	}

	// Integrity: a corrupt page is invisible until a query trips over it, and
	// the whole point of this command is to be the first thing run.
	var integrity string
	if err := d.R.QueryRowContext(ctx, "PRAGMA quick_check(1)").Scan(&integrity); err != nil {
		bad("integrity check did not run: %v", err)
	} else if integrity != "ok" {
		bad("SQLite integrity check: %s", integrity)
	} else {
		ok("SQLite integrity check passed")
	}
	// Foreign keys are only enforced when a connection asks; the check reports
	// rows that would not pass, whatever this connection does.
	var violations int
	if rows, err := d.R.QueryContext(ctx, "SELECT COUNT(*) FROM pragma_foreign_key_check"); err != nil {
		warn("foreign keys check did not run: %v", err)
	} else {
		if rows.Next() {
			_ = rows.Scan(&violations)
		}
		_ = rows.Close()
		if violations > 0 {
			bad("%d row(s) point at missing parents (foreign key violations)", violations)
		} else {
			ok("no foreign key violations")
		}
	}
	// Space: deleted rows never give their space back on their own, and a full
	// volume looks like every other failure.
	var pages, freePages, pageSize int64
	_ = d.R.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages)
	_ = d.R.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&freePages)
	_ = d.R.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize)
	if pages > 0 && pageSize > 0 {
		used := (pages - freePages) * pageSize
		wasted := freePages * pageSize
		if wasted > 256<<20 {
			warn("%s of the file is free space inside it: a VACUUM would return it", humanBytes(wasted))
		} else {
			ok("%s used, %s free space inside the file", humanBytes(used), humanBytes(wasted))
		}
	}
	// A WAL left beside a restored database belongs to the file that was
	// replaced: SQLite would try to recover it into the new one.
	if info, err := os.Stat(path + "-wal"); err == nil && time.Since(info.ModTime()) > 24*time.Hour {
		warn("a stale %s-wal from %s sits beside the database: remove it if no HuntDeck is running", filepath.Base(path), info.ModTime().Format(time.DateOnly))
	}
	if err := d.R.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(new(string)); err != nil {
		warn("journal mode: %v", err)
	}
	var busy int
	if err := d.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master").Scan(&busy); err == nil && busy == 0 {
		warn("the database is empty")
	}
}

// humanBytes is a size as a person reads it.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// humanDuration is an age as a person reads it: "3h", "27m", "4d".
func humanDuration(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%.0fd", d.Hours()/24)
	case d >= time.Hour:
		return fmt.Sprintf("%.0fh", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%.0fm", d.Minutes())
	default:
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
}

// doctorFreshness says how current the instance is: when each platform was last
// read, when the queue was last rebuilt, and how many targets it holds. A
// sound database with a week-old poll is the failure this catches.
func doctorFreshness(ctx context.Context, path string, ok, warn, bad func(string, ...any)) {
	d, err := db.Open(ctx, path)
	if err != nil {
		return // doctorDatabase already reported the open failure
	}
	defer func() { _ = d.Close() }()
	s := store.New(d, time.Now)
	// The decision model is a dependency now: a scan handoff asks it, and so do
	// the policy and triage commands. When it is unreachable those features
	// degrade quietly, so the doctor says which it is.
	checkDecisionModel(ctx, s, filepath.Dir(path), ok, warn)
	checkScanner(ctx, s, filepath.Dir(path), ok, warn)
	checkProxy(ctx, s, filepath.Dir(path), ok, warn)

	if checked, err := s.PlatformsChecked(ctx); err == nil && len(checked) > 0 {
		newest, oldest := time.Time{}, time.Time{}
		for _, c := range checked {
			if c.At.IsZero() {
				continue
			}
			if newest.IsZero() || c.At.After(newest) {
				newest = c.At
			}
			if oldest.IsZero() || c.At.Before(oldest) {
				oldest = c.At
			}
		}
		if !oldest.IsZero() {
			switch age := time.Since(oldest); {
			case age > 24*time.Hour:
				warn("the oldest platform was read %s ago: are polls running?", humanDuration(age))
			case age > 6*time.Hour:
				warn("the oldest platform was read %s ago", humanDuration(age))
			default:
				ok("every platform read within %s", humanDuration(time.Since(newest)))
			}
		}
	}
	if days, err := s.ScoreDays(ctx); err == nil && len(days) > 0 {
		// The backtest's acceptance line needs eight cuts; say how many exist so
		// "is the queue judged yet?" is answerable without the design doc.
		switch {
		case len(days) < 8:
			ok("score history: %d day(s), newest %s (the backtest wants 8)", len(days), days[len(days)-1])
		default:
			ok("score history: %d day(s), newest %s", len(days), days[len(days)-1])
		}
	}
	if qdays, err := s.QueueDays(ctx); err == nil {
		// The queue's own history is the second clock: comparing it with HuntScore
		// starts at two cuts, the verdict needs eight.
		switch {
		case len(qdays) == 0:
			ok("queue history: no snapshot yet (the first rebuild writes one)")
		case len(qdays) < 2:
			ok("queue history: %d day(s), newest %s (the comparison starts at 2)", len(qdays), qdays[len(qdays)-1])
		case len(qdays) < 8:
			ok("queue history: %d day(s), newest %s (the verdict needs 8, ~%s away)", len(qdays), qdays[len(qdays)-1], humanDuration(time.Duration(8-len(qdays))*24*time.Hour))
		default:
			ok("queue history: %d day(s), newest %s", len(qdays), qdays[len(qdays)-1])
		}
	}
	if n, computed, err := s.HuntQueueCount(ctx, store.QueueFilter{}); err == nil {
		switch {
		case computed.IsZero():
			warn("the queue has never been built")
		case time.Since(computed) > 2*time.Hour:
			warn("the queue was rebuilt %s ago (%d targets): is the server running?", humanDuration(time.Since(computed)), n)
		default:
			dropped, dropErr := s.QueueDropped(ctx)
			switch {
			case dropErr != nil:
				ok("queue: %d targets, rebuilt %s ago (the left-out count could not be read)", n, humanDuration(time.Since(computed)))
			case dropped > 0:
				ok("queue: %d targets, rebuilt %s ago, %d left out under the floor", n, humanDuration(time.Since(computed)), dropped)
			default:
				ok("queue: %d targets, rebuilt %s ago, nothing left out", n, humanDuration(time.Since(computed)))
			}
		}
	}
}

// doctorProxy checks that a configured proxy is reachable. HUNTDECK_PROXY
// applies to every platform request, so one dead proxy stops all polling, and
// the failure only showed up as a wall of "giving up after 5 attempts" in the
// poll history. One dial here says it in a line.
func doctorProxy(ctx context.Context, ok, bad func(string, ...any)) {
	raw := strings.TrimSpace(os.Getenv("HUNTDECK_PROXY"))
	if raw == "" {
		return
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		bad("HUNTDECK_PROXY is not a URL: %q", raw)
		return
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "80")
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		bad("HUNTDECK_PROXY %s is not reachable (%v): every platform request goes through it", host, err)
		return
	}
	_ = conn.Close()
	ok("proxy %s is reachable", host)
}

// doctorAI says whether the optional model is set up and how many readings it has
// left behind. Silence would be worse: with no endpoint the program pages quietly
// show only the rules quoted from the policy.
func doctorAI(ctx context.Context, path string, ok, _ func(string, ...any)) {
	base, model := strings.TrimSpace(os.Getenv("HUNTDECK_AI_BASE_URL")), strings.TrimSpace(os.Getenv("HUNTDECK_AI_MODEL"))
	if base == "" || model == "" {
		// Optional, so not a warning: every page works without it.
		ok("ai policy extraction: off (optional; needs HUNTDECK_AI_BASE_URL and HUNTDECK_AI_MODEL)")
		return
	}
	d, err := db.Open(ctx, path)
	if err != nil {
		return
	}
	defer func() { _ = d.Close() }()
	var n int
	if err := d.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM program_ai_rules").Scan(&n); err != nil {
		return
	}
	ok("ai policy extraction: on (%s), %d cached reading(s)", model, n)
}

// checkDecisionModel reports whether the decision model is configured and, when
// it is, whether it answers through whatever proxy it was given. It costs one
// small call with a short timeout: a doctor that hangs is worse than one that
// says "not checked".
func checkDecisionModel(ctx context.Context, s *store.Store, dir string, ok, warn func(string, ...any)) {
	// The key may live in Settings rather than the environment: load the same
	// way the server does, or the doctor would report "not configured" on an
	// instance that is configured.
	cfg, err := overrides.New(s, dir, config.OSSource).Config(ctx)
	if err != nil {
		warn("decision model: the configuration could not be read (%v)", err)
		return
	}
	if cfg.JevKey.Reveal() == "" {
		ok("decision model: not configured (scan handoffs and the policy check run on their own checks)")
		return
	}
	// Timeout, not HTTP: a client supplied in HTTP carries its own transport and
	// would drop the proxy this deployment needs (measured: 451 direct, 403
	// through it).
	client := &jev.Client{BaseURL: cfg.JevURL, Key: cfg.JevKey.Reveal(), Model: cfg.JevModel, Proxy: cfg.JevProxy, Provider: cfg.JevProvider, AccountID: cfg.JevAccountID, WorkspaceID: cfg.JevWorkspaceID,
		Timeout: 6 * time.Second}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	p, model, err := client.Probability(ctx, map[string]any{"probe": "a health check"}, "Is this a health check?")
	_ = p
	if err != nil {
		warn("decision model: %s does not answer (%v)", cfg.JevURL, err)
		return
	}
	ok("decision model: %s (%s) answered as %s in %s", cfg.JevProvider, cfg.JevURL, model, humanDuration(time.Since(start)))
}

// checkScanner reports whether the scan handoff can actually reach ScopeSentry,
// and how loaded its nodes are.
//
// It exists because the doctor said "no problems found" while the scanner key was
// disabled on the other side: every handoff answered 403, the page showed a red
// line, and the diagnostic tool reported a healthy instance. A dependency that
// can be switched off remotely belongs in the doctor.
func checkScanner(ctx context.Context, s *store.Store, dir string, ok, warn func(string, ...any)) {
	cfg, err := overrides.New(s, dir, config.OSSource).Config(ctx)
	if err != nil {
		warn("scanner: the configuration could not be read (%v)", err)
		return
	}
	if cfg.ScanKey.Reveal() == "" || cfg.ScanURL == "" {
		ok("scanner: not configured (no handoff is offered)")
		return
	}
	client := &scopesentry.Client{BaseURL: cfg.ScanURL, Key: cfg.ScanKey.Reveal()}
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	nodes, err := client.Nodes(probe)
	if err != nil {
		// The scanner's own words, verbatim: "API key is disabled" is the one
		// that matters and it is not a transport failure.
		warn("scanner: %s does not answer (%v)", cfg.ScanURL, err)
		return
	}
	if len(nodes) == 0 {
		warn("scanner: reachable but no node is registered: a handoff would have nowhere to go")
		return
	}
	online, full, queued := 0, 0, 0
	for _, n := range nodes {
		if !n.Online() {
			continue
		}
		online++
		queued += n.QueueLen
		if n.QueueLen >= scanhandoff.MaxQueuePerNode {
			full++
		}
	}
	switch {
	case online == 0:
		warn("scanner: %d node(s), none online: a handoff would wait", len(nodes))
	case full == online:
		warn("scanner: all %d online node(s) at their queue limit (%d task(s) waiting): a handoff would be refused", online, queued)
	default:
		ok("scanner: %d of %d node(s) online, %d task(s) queued, %d node(s) with room",
			online, len(nodes), queued, online-full)
	}
}

// checkProxy reports whether the proxy this install is configured to use actually
// carries traffic, and where it comes out.
//
// It earns its line: every platform failing at once looks like six broken
// integrations, and the cause has been one dead proxy, one empty variable and one
// node that answered later. Without this, the doctor said "no problems found"
// while nothing could reach anything.
func checkProxy(ctx context.Context, s *store.Store, dir string, ok, warn func(string, ...any)) {
	cfg, err := overrides.New(s, dir, config.OSSource).Config(ctx)
	if err != nil {
		warn("proxy: the configuration could not be read (%v)", err)
		return
	}
	if cfg.Proxy == nil {
		ok("proxy: direct (no HUNTDECK_PROXY set)")
		return
	}
	// A request through the proxy, and one to a platform: the first says the
	// proxy answers, the second says the routing that matters works.
	client := &http.Client{
		Timeout:   12 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(cfg.Proxy)},
	}
	start := time.Now()
	res, err := client.Get("https://api.ipify.org")
	if err != nil {
		warn("proxy: %s does not carry traffic (%v)", cfg.Proxy.Host, err)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64))
	_ = res.Body.Close()
	exit := strings.TrimSpace(string(body))
	took := time.Since(start).Round(time.Millisecond)
	if res.StatusCode != http.StatusOK || exit == "" {
		warn("proxy: %s answered %d for the exit-address probe", cfg.Proxy.Host, res.StatusCode)
		return
	}
	plat, err := client.Get("https://app.intigriti.com/")
	if err != nil {
		warn("proxy: exit %s works, but a platform request failed (%v): check the routing rules", exit, err)
		return
	}
	_ = plat.Body.Close()
	ok("proxy: %s → %s in %s · app.intigriti.com %d (the rules send it through the same proxy)",
		cfg.Proxy.Host, exit, took, plat.StatusCode)
}
