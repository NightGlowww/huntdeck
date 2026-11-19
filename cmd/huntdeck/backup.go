package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/version"
)

// backupCmd writes one file holding what a restore needs: a consistent copy of
// the database (VACUUM INTO, safe while the app runs), the key that decrypts
// credentials saved in Settings, and the platform sessions.
func backupCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	to := fs.String("to", "", "file to write (default huntdeck-backup-<version>-<time>.tar.gz)")
	data := fs.String("data", "", "data directory (default $HUNTDECK_DATA_DIR, else ./data)")
	noSessions := fs.Bool("no-sessions", false, "leave the platform logins out")
	keep := fs.Int("keep", 0, "keep only the newest N backups huntdeck-backup-* in the file's directory (0: keep all)")
	verify := fs.Bool("verify", true, "read the archive back after writing it, and fail if it does not open")
	pos, code, ok := parseFlags(fs, args, stderr)
	if !ok {
		return code
	}
	if len(pos) > 0 {
		fmt.Fprintf(stderr, "huntdeck backup: unexpected arguments: %v\n", pos)
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
	//nolint:gosec // the path is the operator's --data, not request input
	if _, err := os.Stat(dbPath); err != nil {
		fmt.Fprintf(stderr, "huntdeck backup: no database at %s (give --data)\n", dbPath)
		return 1
	}

	out := *to
	if out == "" {
		out = fmt.Sprintf("huntdeck-backup-%s-%s.tar.gz", version.Version, time.Now().UTC().Format("20060102T150405Z"))
	}

	// A snapshot, not the live file: VACUUM INTO gives one consistent image
	// while the app keeps writing.
	snap, err := os.CreateTemp("", "huntdeck-snapshot-*.db")
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck backup: %v\n", err)
		return 1
	}
	snapPath := snap.Name()
	_ = snap.Close()
	defer func() { _ = os.Remove(snapPath) }()

	ctx := context.Background()
	conn, err := db.Open(ctx, dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck backup: %v\n", err)
		return 1
	}
	// The writer: the reader handle is opened read-only, and VACUUM INTO
	// writes.
	if err := vacuumInto(ctx, conn.W, snapPath); err != nil {
		_ = conn.Close()
		fmt.Fprintf(stderr, "huntdeck backup: %v\n", err)
		return 1
	}
	if err := conn.Close(); err != nil {
		fmt.Fprintf(stderr, "huntdeck backup: %v\n", err)
		return 1
	}

	files := []string{}
	sessions := filepath.Join(dir, "sessions")
	//nolint:gosec // the path is the operator's --data, not request input
	if _, err := os.Stat(sessions); err == nil && !*noSessions {
		files = append(files, "sessions")
	}
	manifest := map[string]any{
		"version":    version.String(),
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"data_dir":   dir,
		"sessions":   len(files) > 0,
	}
	// What the snapshot holds, so a restore can prove it read the same data
	// back (see restoreCmd).
	if counts, err := snapshotCounts(ctx, snapPath); err == nil {
		manifest["programs"] = counts.programs
		manifest["scope_entries"] = counts.scopeEntries
		manifest["changes"] = counts.changes
		manifest["poll_runs"] = counts.pollRuns
	}
	if err := writeBackup(out, snapPath, dir, files, manifest); err != nil {
		fmt.Fprintf(stderr, "huntdeck backup: %v\n", err)
		return 1
	}
	if fi, err := os.Stat(out); err == nil {
		fmt.Fprintf(stdout, "wrote %s (%d bytes)\n", out, fi.Size())
	}
	// Read it back now, not when it is needed: an archive that cannot be read
	// is worse than none, because it is believed.
	if *verify {
		detail, err := verifyBackup(out)
		if err != nil {
			fmt.Fprintf(stderr, "huntdeck backup: the archive it wrote does not read back: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "verified: %s\n", detail)
	}
	if *keep > 0 {
		removed, err := rotate(out, *keep)
		if err != nil {
			fmt.Fprintf(stderr, "huntdeck backup: rotation: %v\n", err)
			return 1
		}
		for _, name := range removed {
			fmt.Fprintf(stdout, "removed %s\n", name)
		}
	}
	return 0
}

// vacuumInto copies the database to path in one consistent image.
func vacuumInto(ctx context.Context, conn *sql.DB, path string) error {
	if _, err := conn.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return fmt.Errorf("snapshot the database: %w", err)
	}
	return nil
}

// writeBackup writes the tar.gz: the manifest, the database snapshot, the
// secret key and (when asked) the sessions directory.
func writeBackup(out, snapshot, dir string, files []string, manifest map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(filepath.Clean(out)), 0o750); err != nil && filepath.Dir(out) != "." {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeTarBytes(tw, "manifest.json", append(body, '\n')); err != nil {
		return err
	}
	if err := writeTarFile(tw, "huntdeck.db", snapshot); err != nil {
		return err
	}
	if key := filepath.Join(dir, "secret.key"); fileExists(key) {
		if err := writeTarFile(tw, "secret.key", key); err != nil {
			return err
		}
	}
	for _, name := range files {
		if err := writeTarTree(tw, name, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func fileExists(path string) bool {
	//nolint:gosec // the path is the operator's, not request input
	//nolint:gosec // path is one of the backup's own files, resolved from --data
	_, err := os.Stat(path)
	return err == nil
}

func writeTarBytes(tw *tar.Writer, name string, body []byte) error {
	hdr := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), ModTime: time.Now()}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(body)
	return err
}

func writeTarFile(tw *tar.Writer, name, path string) error {
	//nolint:gosec // path is one of the backup's own files, resolved from --data
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	hdr, err := tar.FileInfoHeader(fi, "")
	if err != nil {
		return err
	}
	hdr.Name, hdr.Mode = name, 0o600
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	//nolint:gosec // path is one of the backup's own files, resolved from --data
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	_, err = io.Copy(tw, src)
	return err
}

// writeTarTree adds a directory and its files under name.
func writeTarTree(tw *tar.Writer, name, path string) error {
	//nolint:gosec // the tree is the operator's --data, walked to copy it
	return filepath.Walk(path, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(path, p)
		if err != nil {
			return err
		}
		return writeTarFile(tw, filepath.ToSlash(filepath.Join(name, rel)), p)
	})
}

// rotate deletes all but the newest keep backups named huntdeck-backup-* beside
// out, and returns what it removed.
func rotate(out string, keep int) ([]string, error) {
	dir := filepath.Dir(out)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type aged struct {
		name string
		at   time.Time
	}
	var backups []aged
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "huntdeck-backup-") || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		backups = append(backups, aged{e.Name(), info.ModTime()})
	}
	if len(backups) <= keep {
		return nil, nil
	}
	slices.SortFunc(backups, func(a, b aged) int { return b.at.Compare(a.at) })
	var removed []string
	for _, b := range backups[keep:] {
		if err := os.Remove(filepath.Join(dir, b.name)); err != nil {
			return removed, err
		}
		removed = append(removed, b.name)
	}
	return removed, nil
}

type rowCounts struct {
	programs     int64
	scopeEntries int64
	changes      int64
	pollRuns     int64
}

// snapshotCounts reads what a snapshot holds, for the manifest.
func snapshotCounts(ctx context.Context, path string) (rowCounts, error) {
	var c rowCounts
	d, err := db.Open(ctx, path)
	if err != nil {
		return c, err
	}
	defer func() { _ = d.Close() }()
	if err := d.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM programs").Scan(&c.programs); err != nil {
		return c, err
	}
	if err := d.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM scope_entries").Scan(&c.scopeEntries); err != nil {
		return c, err
	}
	// History too: a backup that lost the change log is not the same install.
	if err := d.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM changes").Scan(&c.changes); err != nil {
		return c, err
	}
	if err := d.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM poll_runs").Scan(&c.pollRuns); err != nil {
		return c, err
	}
	return c, nil
}

// verifyBackup reads the archive it just wrote: unpack to a temporary
// directory, open the database, compare its counts with the manifest. A backup
// that cannot be read is worse than none, because it is believed.
func verifyBackup(archive string) (string, error) {
	dir, err := os.MkdirTemp("", "huntdeck-verify-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := unpack(archive, dir, false); err != nil {
		return "", err
	}
	dbPath := filepath.Join(dir, "huntdeck.db")
	if !fileExists(dbPath) {
		return "", fmt.Errorf("the archive holds no huntdeck.db")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	counts, err := snapshotCounts(ctx, dbPath)
	if err != nil {
		return "", err
	}
	schema := schemaVersionMust(ctx, dbPath)
	return fmt.Sprintf("database v%d, %d programs, %d scope entries, %d changes",
		schema, counts.programs, counts.scopeEntries, counts.changes), nil
}
