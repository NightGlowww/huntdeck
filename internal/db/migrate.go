package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var embedded embed.FS

// Migration is one numbered schema change.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

var migrationName = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// Migrations returns the migrations built into the binary.
func Migrations() ([]Migration, error) {
	sub, err := fs.Sub(embedded, "migrations")
	if err != nil {
		return nil, err
	}
	return LoadMigrations(sub)
}

// LoadMigrations reads NNNN_name.sql files from fsys. Versions must start at
// 1 and have no gaps, so a missing or misnumbered file fails loudly.
func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var out []Migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration %q: name must look like 0001_description.sql", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", e.Name(), err)
		}
		out = append(out, Migration{Version: v, Name: m[2], SQL: string(body)})
	}
	slices.SortFunc(out, func(a, b Migration) int { return a.Version - b.Version })
	for i, m := range out {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migration %04d_%s: expected version %04d (versions must be contiguous from 0001)", m.Version, m.Name, i+1)
		}
	}
	return out, nil
}

// Migrate applies pending migrations in order, each in its own transaction.
// If the database already has a schema and migrations are pending, a backup
// is written to backupDir first. It returns the number applied.
func (d *DB) Migrate(ctx context.Context, migs []Migration, backupDir string, log *slog.Logger) (int, error) {
	if _, err := d.W.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TEXT NOT NULL
	) STRICT`); err != nil {
		return 0, fmt.Errorf("create schema_migrations: %w", err)
	}

	// The runner owns its own table: the digest column is added here, once,
	// rather than in a migration (a migration recording its own digest would
	// have to run before the column it needs exists).
	if !d.hasColumn(ctx, "schema_migrations", "sha256") {
		if _, err := d.W.ExecContext(ctx, `ALTER TABLE schema_migrations ADD COLUMN sha256 TEXT NOT NULL DEFAULT ''`); err != nil {
			return 0, fmt.Errorf("add migration digest column: %w", err)
		}
	}

	var current int
	if err := d.W.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	if err := d.verifyDigests(ctx, migs[:current]); err != nil {
		return 0, err
	}

	latest := len(migs)
	if current > latest {
		return 0, fmt.Errorf("database schema is version %d but this HuntDeck build only knows up to %d; upgrade HuntDeck", current, latest)
	}
	if current == latest {
		// Nothing to apply, but the copies left behind still need trimming: a
		// backlog is what fills a disk, and this is the path almost every start
		// takes.
		pruneBackups(backupDir, log)
		return 0, nil
	}

	if current > 0 {
		path, err := d.backup(ctx, backupDir, current)
		if err != nil {
			return 0, fmt.Errorf("pre-migration backup: %w", err)
		}
		log.Info("database backed up before migrating", "path", path, "from_version", current, "to_version", latest)
	}

	for _, m := range migs[current:] {
		if err := d.apply(ctx, m); err != nil {
			return m.Version - current - 1, err
		}
		log.Info("applied migration", "version", m.Version, "name", m.Name)
	}
	// Every start, not only a start that migrates: copies left by earlier versions
	// are full databases too, and a backlog is what fills a disk unnoticed.
	pruneBackups(backupDir, log)
	return latest - current, nil
}

func (d *DB) apply(ctx context.Context, m Migration) (err error) {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migration %04d_%s: begin: %w", m.Version, m.Name, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, rollback(tx))
		}
	}()
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return fmt.Errorf("migration %04d_%s: %w", m.Version, m.Name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at, sha256) VALUES (?, ?, ?, ?)`,
		m.Version, m.Name, time.Now().UTC().Format(time.RFC3339), digest(m.SQL)); err != nil {
		return fmt.Errorf("migration %04d_%s: record: %w", m.Version, m.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration %04d_%s: commit: %w", m.Version, m.Name, err)
	}
	return nil
}

func rollback(tx *sql.Tx) error {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("rollback: %w", err)
	}
	return nil
}

// backup writes a consistent copy of the live database using VACUUM INTO.
func (d *DB) backup(ctx context.Context, dir string, version int) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("huntdeck-%s-schema-v%04d.db", time.Now().UTC().Format("20060102T150405Z"), version)
	path := filepath.Join(dir, name)
	if _, err := d.W.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return "", err
	}
	return path, nil
}

// digest is the migration's SQL, hashed: a shipped migration edited afterwards
// changes it.
func digest(sqlText string) string {
	sum := sha256.Sum256([]byte(sqlText))
	return hex.EncodeToString(sum[:])
}

// verifyDigests refuses to run when an applied migration's SQL no longer
// matches what was recorded. Rows written before digests existed have an empty
// hash; those are backfilled once from the embedded SQL, which protects every
// later edit but cannot know about one made before the feature arrived.
func (d *DB) verifyDigests(ctx context.Context, applied []Migration) error {
	// The column arrives with migration 0062; a database upgraded from before it
	// is verified from the next start on.
	if !d.hasColumn(ctx, "schema_migrations", "sha256") {
		return nil
	}
	rows, err := d.R.QueryContext(ctx, `SELECT version, sha256 FROM schema_migrations WHERE version <= ?`, len(applied))
	if err != nil {
		return fmt.Errorf("read migration digests: %w", err)
	}
	stored := map[int]string{}
	for rows.Next() {
		var v int
		var h string
		if err := rows.Scan(&v, &h); err != nil {
			_ = rows.Close()
			return err
		}
		stored[v] = h
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, m := range applied {
		want := digest(m.SQL)
		got, ok := stored[m.Version]
		if !ok {
			continue
		}
		if got == "" {
			if _, err := d.W.ExecContext(ctx, `UPDATE schema_migrations SET sha256 = ? WHERE version = ?`, want, m.Version); err != nil {
				return fmt.Errorf("record migration digest %04d: %w", m.Version, err)
			}
			continue
		}
		if got != want {
			return fmt.Errorf("migration %04d_%s was edited after it shipped (digest %s, recorded %s): migrations are append-only, add a new one", m.Version, m.Name, want[:12], got[:12])
		}
	}
	return nil
}

// hasColumn reports whether a table has the named column.
func (d *DB) hasColumn(ctx context.Context, table, column string) bool {
	rows, err := d.R.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil && name == column {
			return true
		}
	}
	return false
}

// keepBackups is how many pre-migration backups are kept. Each one is a full
// copy of the database (150 MiB at the time of writing) and a schema upgrade
// takes one, so on an instance that upgrades often they accumulate without a
// limit: seventeen of them were 2.3 GiB on a host that also runs a scanner.
//
// Three is the point where a bad upgrade can still be retried by hand and the
// disk is not quietly consumed: the copy from before the upgrade that broke
// something, the one before that, and the current state.
const keepBackups = 3

// pruneBackups deletes the oldest pre-migration copies beyond keepBackups. It is
// best effort: a backup that cannot be removed must not stop a migration.
func pruneBackups(dir string, log *slog.Logger) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type kept struct {
		path string
		at   time.Time
	}
	var copies []kept
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".db") || !strings.HasPrefix(e.Name(), "huntdeck-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		copies = append(copies, kept{filepath.Join(dir, e.Name()), info.ModTime()})
	}
	if len(copies) <= keepBackups {
		return
	}
	slices.SortFunc(copies, func(a, b kept) int { return b.at.Compare(a.at) })
	for _, c := range copies[keepBackups:] {
		if err := os.Remove(c.path); err != nil {
			log.Warn("pre-migration backup not removed", "path", c.path, "err", err)
			continue
		}
		log.Info("old pre-migration backup removed", "path", c.path)
	}
}
