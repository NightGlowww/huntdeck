package store

import (
	"bufio"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/mdpsec/huntdeck/internal/db"
)

// ErrNotEmpty means a seed can't be loaded because the database already
// holds programs; seeds only start an install off.
var ErrNotEmpty = errors.New("the database already holds programs; a seed can only be loaded into a new install")

// SeedCounts is how many rows of each table a seed export or import copied.
type SeedCounts map[string]int64

// String is "programs 1402, scope_entries 74873, …" in copy order.
func (c SeedCounts) String() string {
	var parts []string
	for _, t := range seedTables {
		parts = append(parts, fmt.Sprintf("%s %d", t.Name, c[t.Name]))
	}
	return strings.Join(parts, ", ")
}

// Empty reports whether the database holds no programs yet.
func (s *Store) Empty(ctx context.Context) (bool, error) {
	var some bool
	err := s.db.R.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM programs)`).Scan(&some)
	return !some, err
}

// ExportSeed writes the public programs' platform data (see seedTables) to
// a new database at path, gzipped if path ends in ".gz", narrowed by o.
// migs are the migrations the seed's schema is made with: this build's.
func (s *Store) ExportSeed(ctx context.Context, path string, migs []db.Migration, o SeedOptions) (SeedCounts, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("%s already exists", path)
	}
	file := path
	if strings.HasSuffix(path, ".gz") {
		file = strings.TrimSuffix(path, ".gz") + ".tmp"
		defer removeDB(file)
	}
	if err := newSeedFile(ctx, file, migs); err != nil {
		removeDB(file)
		return nil, err
	}
	n, err := s.copySeed(ctx, file, "main", "seed", o)
	if err == nil {
		err = s.attached(ctx, file, func(conn *sql.Conn) error {
			// Only a signed-out export says it is one (CheckSignedOut).
			if _, err := conn.ExecContext(ctx, `DELETE FROM seed.settings WHERE key = ?`, SignedOutSetting); err != nil {
				return err
			}
			if o.SignedOut {
				at := ts(s.Now())
				if _, err := conn.ExecContext(ctx, `INSERT INTO seed.settings (key, value, updated_at) VALUES (?, ?, ?)`, SignedOutSetting, at, at); err != nil {
					return err
				}
			}
			if _, err := conn.ExecContext(ctx, `VACUUM seed`); err != nil {
				return fmt.Errorf("compact: %w", err)
			}
			_, err := conn.ExecContext(ctx, `PRAGMA seed.journal_mode = DELETE`) // one file to hand out
			return err
		})
	}
	if err == nil && file != path {
		err = gzipFile(file, path)
	}
	if err != nil {
		removeDB(file)
		_ = os.Remove(path)
		return nil, err
	}
	return n, nil
}

// ImportSeed loads a seed written by ExportSeed (gzipped or not) into this
// empty database. The seed is first brought up to this build's schema in a
// copy under tmpDir; one from a newer build is refused. Only what
// seedTables lists is read from it.
func (s *Store) ImportSeed(ctx context.Context, path, tmpDir string, migs []db.Migration) (SeedCounts, error) {
	empty, err := s.Empty(ctx)
	if err != nil {
		return nil, err
	}
	if !empty {
		return nil, ErrNotEmpty
	}
	tmp, err := os.CreateTemp(tmpDir, "seed-*.db")
	if err != nil {
		return nil, err
	}
	file := tmp.Name()
	defer removeDB(file)
	err = unpack(path, tmp)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	backups := file + "-backups"
	defer func() { _ = os.RemoveAll(backups) }()
	if err := migrateFile(ctx, file, migs, backups); err != nil {
		return nil, fmt.Errorf("seed %s: %w", path, err)
	}
	return s.copySeed(ctx, file, "seed", "main", SeedOptions{})
}

// copySeed copies seedTables from schema from to schema to, one of them
// the database at file attached as "seed", in one transaction.
//
// The transaction is deferred, not the writer's usual IMMEDIATE, which
// would lock every attached database for writing: exporting would then
// hold the live database's write lock for its minute or so, and a running
// server's polls would fail. Deferred, it locks only what it writes.
const seedBegin = `BEGIN DEFERRED`

func (s *Store) copySeed(ctx context.Context, file, from, to string, o SeedOptions) (SeedCounts, error) {
	n := SeedCounts{}
	err := s.attached(ctx, file, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, seedBegin); err != nil {
			return err
		}
		for _, t := range seedTables {
			res, err := conn.ExecContext(ctx, t.copySQL(from, to, o))
			if err != nil {
				_, rerr := conn.ExecContext(context.WithoutCancel(ctx), `ROLLBACK`)
				return errors.Join(fmt.Errorf("copy %s: %w", t.Name, err), rerr)
			}
			n[t.Name], _ = res.RowsAffected()
		}
		_, err := conn.ExecContext(ctx, `COMMIT`)
		return err
	})
	return n, err
}

// copySQL is the INSERT … SELECT copying t from schema from to schema to,
// narrowed by o.
func (t seedTable) copySQL(from, to string, o SeedOptions) string {
	cols := make([]string, len(t.Cols))
	exprs := make([]string, len(t.Cols))
	for i, c := range t.Cols {
		cols[i], exprs[i] = c, c
		if name, expr, ok := strings.Cut(c, "="); ok {
			cols[i], exprs[i] = name, expr
		}
	}
	verb := "INSERT"
	if t.Replace {
		verb = "INSERT OR REPLACE"
	}
	return fmt.Sprintf(`%s INTO %s.%s (%s) SELECT %s FROM %s.%s WHERE %s`, verb, to, t.Name, strings.Join(cols, ", "),
		strings.Join(exprs, ", "), from, t.Name, t.where(from, o))
}

// where is t's filter for rows read from schema from.
func (t seedTable) where(from string, o SeedOptions) string {
	only, changes := "", ""
	if o.SignedOut {
		only, changes = seedSignedOut, seedSignedOutChanges
	}
	w := strings.NewReplacer("{only}", only, "{changes}", changes).Replace(t.Where)
	return strings.ReplaceAll(w, "{src}", from) // after: the filters name {src} too
}

// attached runs fn on the writer connection with file attached as "seed".
func (s *Store) attached(ctx context.Context, file string, fn func(*sql.Conn) error) error {
	conn, err := s.db.W.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS seed`, file); err != nil {
		return fmt.Errorf("attach seed: %w", err)
	}
	err = fn(conn)
	if _, derr := conn.ExecContext(context.WithoutCancel(ctx), `DETACH DATABASE seed`); err == nil && derr != nil {
		err = fmt.Errorf("detach seed: %w", derr)
	}
	return err
}

// newSeedFile creates the database at file with this build's schema.
func newSeedFile(ctx context.Context, file string, migs []db.Migration) error {
	if _, err := os.Stat(file); err == nil {
		return fmt.Errorf("%s already exists", file)
	}
	return migrateFile(ctx, file, migs, filepath.Dir(file))
}

// migrateFile brings the database at file up to migs. backups is where an
// older one is copied first (Migrate always keeps a copy).
func migrateFile(ctx context.Context, file string, migs []db.Migration, backups string) error {
	d, err := db.Open(ctx, file)
	if err != nil {
		return err
	}
	_, err = d.Migrate(ctx, migs, backups, slog.New(slog.DiscardHandler))
	return errors.Join(err, d.Close())
}

// unpack copies the seed at path to w, gunzipping it if it's gzipped.
func unpack(path string, w io.Writer) error {
	return unpackLimit(path, w, maxSeedUnpacked)
}

// unpackLimit is unpack with the ceiling given, so a test can use a small one.
func unpackLimit(path string, w io.Writer, limit int64) error {
	f, err := os.Open(path) //nolint:gosec // a file the user named
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	br := bufio.NewReader(f)
	var r io.Reader = br
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return err
		}
		defer func() { _ = gz.Close() }()
		r = gz
	}
	// A seed that unpacks past the ceiling is a bomb or a mistake, not a
	// database: stop at the limit instead of filling the volume.
	n, err := io.Copy(w, io.LimitReader(r, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("seed unpacks past its ceiling of %d MiB: refusing it", limit>>20)
	}
	return nil
}

// maxSeedUnpacked bounds what a seed may expand to. Today's seeds are ~150 MB
// of database; a gigabyte is generous and still small against a disk.
const maxSeedUnpacked = 8 << 30

func gzipFile(src, dst string) (err error) {
	in, err := os.Open(src) //nolint:gosec // our own temporary file
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // a file the user named
	if err != nil {
		return err
	}
	gz, _ := gzip.NewWriterLevel(out, gzip.BestCompression)
	if _, err = io.Copy(gz, in); err == nil {
		err = gz.Close()
	}
	return errors.Join(err, out.Close())
}

// removeDB deletes a database file and its WAL companions.
func removeDB(file string) {
	for _, f := range []string{file, file + "-wal", file + "-shm", file + "-journal"} {
		_ = os.Remove(f)
	}
}
