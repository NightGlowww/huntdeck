package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mdpsec/huntdeck/internal/db"
)

// restoreCmd unpacks a backup written by "huntdeck backup" into a data
// directory. It refuses to write over an existing install unless --force, and
// refuses a database that needs migrations this binary doesn't have: a backup
// from a newer HuntDeck can't be read by an older one.
func restoreCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	from := fs.String("from", "", "backup file to read")
	data := fs.String("data", "", "data directory (default $HUNTDECK_DATA_DIR, else ./data)")
	force := fs.Bool("force", false, "write over an existing database")
	noSessions := fs.Bool("no-sessions", false, "leave the platform logins out")
	dryRun := fs.Bool("dry-run", false, "check the archive and the schema, write nothing")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *from == "" || fs.NArg() > 0 {
		fmt.Fprintln(stderr, "usage: huntdeck restore --from FILE [--data DIR] [--force] [--no-sessions]")
		return 2
	}
	dir := *data
	if dir == "" {
		dir = os.Getenv("HUNTDECK_DATA_DIR")
	}
	if dir == "" {
		dir = "data"
	}
	dbPath := filepath.Join(dir, "huntdeck.db")
	if fileExists(dbPath) && !*force && !*dryRun {
		fmt.Fprintf(stderr, "huntdeck restore: %s exists; pass --force to replace it (stop HuntDeck first)\n", dbPath)
		return 1
	}

	// Unpack beside the target first, so a broken archive leaves nothing behind.
	// A dry run stages in the temp directory: it must not touch the target at
	// all.
	stagingBase := dir
	if *dryRun {
		stagingBase = os.TempDir()
		//nolint:gosec // the operator's --data directory, created 0700
	} else if err := os.MkdirAll(dir, 0o700); err != nil { //nolint:gosec // the operator's --data directory
		fmt.Fprintf(stderr, "huntdeck restore: %v\n", err)
		return 1
	}
	// Refuse while a server is running: it would keep writing into the inode
	// this restore is about to replace. Taken after the directory exists, and
	// only when something will actually be written.
	if !*dryRun {
		if release, err := lockData(dir); err != nil {
			fmt.Fprintf(stderr, "huntdeck restore: %v\n", err)
			return 1
		} else {
			defer release()
		}
	}
	staging, err := os.MkdirTemp(stagingBase, ".restore-*")
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck restore: %v\n", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := unpack(*from, staging, *noSessions); err != nil {
		fmt.Fprintf(stderr, "huntdeck restore: %v\n", err)
		return 1
	}
	stagedDB := filepath.Join(staging, "huntdeck.db")
	if !fileExists(stagedDB) {
		fmt.Fprintln(stderr, "huntdeck restore: the archive holds no huntdeck.db")
		return 1
	}
	if err := checkSchema(context.Background(), stagedDB); err != nil {
		fmt.Fprintf(stderr, "huntdeck restore: %v\n", err)
		return 1
	}
	// The archive says what it holds; the database in it must agree, or it is
	// damaged in a way that only shows up later.
	if err := verifyCounts(context.Background(), stagedDB, staging); err != nil {
		fmt.Fprintf(stderr, "huntdeck restore: %v\n", err)
		return 1
	}

	if *dryRun {
		fmt.Fprintf(stdout, "%s would restore into %s: database v%d, %s; nothing written\n", *from, dir,
			schemaVersionMust(context.Background(), stagedDB), sessionsNote(staging, *noSessions))
		return 0
	}

	// A WAL beside the database belongs to the file being replaced: SQLite would
	// replay it into the restored one and mix pages. It goes with the old file.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(dbPath + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "huntdeck restore: remove %s: %v\n", dbPath+suffix, err)
			return 1
		}
	}

	// Move into place: the database last, so a half-finished restore is never
	// mistaken for a complete one.
	for _, name := range []string{"secret.key", "sessions"} {
		src := filepath.Join(staging, name)
		if !fileExists(src) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil { //nolint:gosec // the data directory's own entries
			fmt.Fprintf(stderr, "huntdeck restore: %v\n", err)
			return 1
		}
		if err := os.Rename(src, filepath.Join(dir, name)); err != nil {
			fmt.Fprintf(stderr, "huntdeck restore: %v\n", err)
			return 1
		}
	}
	if err := os.Rename(stagedDB, dbPath); err != nil { //nolint:gosec // the operator's data directory plus a constant name
		fmt.Fprintf(stderr, "huntdeck restore: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "restored %s into %s\n", *from, dir)
	if fileExists(filepath.Join(dir, "secret.key")) {
		fmt.Fprintln(stdout, "secret.key restored: credentials saved in Settings will decrypt")
	} else {
		fmt.Fprintln(stdout, "no secret.key in the archive: credentials saved in Settings will not decrypt")
	}
	return 0
}

// unpack writes the archive's regular files under dir, keeping their names.
func unpack(from, dir string, noSessions bool) error {
	f, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("read %s: %w", from, err)
	}
	tr := tar.NewReader(gz)
	// An archive is unpacked into the data directory: a bomb should fail here,
	// not fill the volume.
	var unpacked int64
	const maxUnpacked = 8 << 30
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(hdr.Name)
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// manifest.json is unpacked too: verifyCounts reads the counts it
		// claims, and it stays behind in the staging directory.
		if strings.Contains(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("refusing path %q in the archive", hdr.Name)
		}
		if noSessions && (name == "sessions" || strings.HasPrefix(name, "sessions"+string(filepath.Separator))) {
			continue
		}
		target := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		n, err := io.Copy(out, io.LimitReader(tr, maxUnpacked-unpacked+1))
		unpacked += n
		if err != nil {
			_ = out.Close()
			return err
		}
		if unpacked > maxUnpacked {
			_ = out.Close()
			return fmt.Errorf("the archive unpacks to more than %d GiB: refusing it", maxUnpacked>>30)
		}
		if err := out.Close(); err != nil {
			return err
		}
	}
}

// checkSchema refuses a database that needs migrations this binary lacks: it
// would look fine until the first query against a newer column.
func checkSchema(ctx context.Context, path string) error {
	d, err := db.Open(ctx, path)
	if err != nil {
		return fmt.Errorf("open the backup's database: %w", err)
	}
	defer func() { _ = d.Close() }()
	version, err := schemaVersion(ctx, d)
	if err != nil {
		return err
	}
	migs, err := db.Migrations()
	if err != nil {
		return err
	}
	if version > int64(len(migs)) {
		return fmt.Errorf("the backup is schema v%d, newer than this HuntDeck (v%d): use a newer build", version, len(migs))
	}
	return nil
}

func schemaVersion(ctx context.Context, d *db.DB) (int64, error) {
	var n sql.NullInt64
	if err := d.R.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&n); err != nil {
		return 0, fmt.Errorf("read the backup's schema version: %w", err)
	}
	return n.Int64, nil
}

// schemaVersionMust reads a database's schema version, for the dry run's report.
func schemaVersionMust(ctx context.Context, path string) int64 {
	d, err := db.Open(ctx, path)
	if err != nil {
		return 0
	}
	defer func() { _ = d.Close() }()
	v, err := schemaVersion(ctx, d)
	if err != nil {
		return 0
	}
	return v
}

// sessionsNote says whether the archive carries the platform logins.
func sessionsNote(staging string, noSessions bool) string {
	if noSessions {
		return "sessions left out"
	}
	if fileExists(filepath.Join(staging, "sessions")) {
		return "with the platform sessions"
	}
	return "no platform sessions"
}

// verifyCounts compares a staged database with the counts its manifest claims.
// A manifest without counts (an older backup) is accepted as is.
func verifyCounts(ctx context.Context, dbPath, staging string) error {
	manifest := filepath.Join(staging, "manifest.json")
	if !fileExists(manifest) {
		return nil
	}
	body, err := os.ReadFile(manifest)
	if err != nil {
		return err
	}
	var m struct {
		Programs     *int64 `json:"programs"`
		ScopeEntries *int64 `json:"scope_entries"`
		Changes      *int64 `json:"changes"`
		PollRuns     *int64 `json:"poll_runs"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return fmt.Errorf("read the manifest: %w", err)
	}
	if m.Programs == nil && m.ScopeEntries == nil && m.Changes == nil && m.PollRuns == nil {
		return nil
	}
	d, err := db.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	if m.Programs != nil {
		var n int64
		if err := d.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM programs").Scan(&n); err != nil {
			return err
		}
		if n != *m.Programs {
			return fmt.Errorf("the archive says %d programs, the database in it has %d: it is damaged", *m.Programs, n)
		}
	}
	if m.ScopeEntries != nil {
		var n int64
		if err := d.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM scope_entries").Scan(&n); err != nil {
			return err
		}
		if n != *m.ScopeEntries {
			return fmt.Errorf("the archive says %d scope entries, the database in it has %d: it is damaged", *m.ScopeEntries, n)
		}
	}
	if m.Changes != nil {
		var n int64
		if err := d.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM changes").Scan(&n); err != nil {
			return err
		}
		if n != *m.Changes {
			return fmt.Errorf("the archive says %d changes, the database in it has %d: it is damaged", *m.Changes, n)
		}
	}
	if m.PollRuns != nil {
		var n int64
		if err := d.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM poll_runs").Scan(&n); err != nil {
			return err
		}
		if n != *m.PollRuns {
			return fmt.Errorf("the archive says %d polls, the database in it has %d: it is damaged", *m.PollRuns, n)
		}
	}
	return nil
}
