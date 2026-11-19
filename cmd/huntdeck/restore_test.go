package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/db"
)

// A backup restores into a fresh directory, and refuses to write over an
// existing install without --force.
func TestBackupRestoreRoundTrip(t *testing.T) {
	src := t.TempDir()
	makeInstall(t, src)
	archive := filepath.Join(t.TempDir(), "b.tar.gz")
	var out, errOut strings.Builder
	if code := backupCmd([]string{"--data", src, "--to", archive}, &out, &errOut); code != 0 {
		t.Fatalf("backup: %d %s", code, errOut.String())
	}

	dst := t.TempDir()
	if code := restoreCmd([]string{"--from", archive, "--data", dst}, &out, &errOut); code != 0 {
		t.Fatalf("restore: %d %s", code, errOut.String())
	}
	if !fileExists(filepath.Join(dst, "huntdeck.db")) || !fileExists(filepath.Join(dst, "secret.key")) {
		t.Fatalf("restore left %v", dst)
	}
	if !fileExists(filepath.Join(dst, "sessions", "hackerone", "cookies.json")) {
		t.Error("the sessions were not restored")
	}
	// The restored database answers a query.
	d, err := db.Open(context.Background(), filepath.Join(dst, "huntdeck.db"))
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.R.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM programs").Scan(&n); err != nil {
		t.Fatalf("restored database: %v", err)
	}
	_ = d.Close()

	// A second restore over the same directory needs --force.
	errOut.Reset()
	if code := restoreCmd([]string{"--from", archive, "--data", dst}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "--force") {
		t.Errorf("restore over an existing database = %d %q", code, errOut.String())
	}
	if code := restoreCmd([]string{"--from", archive, "--data", dst, "--force"}, &out, &errOut); code != 0 {
		t.Errorf("restore --force: %d %s", code, errOut.String())
	}

	// --no-sessions leaves the logins behind.
	noSessions := t.TempDir()
	if code := restoreCmd([]string{"--from", archive, "--data", noSessions, "--no-sessions"}, &out, &errOut); code != 0 {
		t.Fatalf("restore --no-sessions: %d %s", code, errOut.String())
	}
	if fileExists(filepath.Join(noSessions, "sessions")) {
		t.Error("--no-sessions restored the sessions")
	}
}

// makeInstall builds a data directory with a migrated database, a key and one
// session file.
func makeInstall(t *testing.T, dir string) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(dir, "huntdeck.db"))
	if err != nil {
		t.Fatal(err)
	}
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Migrate(ctx, migs, t.TempDir(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	if err := os.WriteFile(filepath.Join(dir, "secret.key"), []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sessions", "hackerone"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions", "hackerone", "cookies.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A backup from a newer HuntDeck is refused: its schema needs migrations this
// binary doesn't have.
func TestRestoreRefusesNewerSchema(t *testing.T) {
	src := t.TempDir()
	makeInstall(t, src)
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(src, "huntdeck.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.W.ExecContext(ctx, "INSERT INTO schema_migrations (version, name, applied_at) VALUES (999, 'future', '2026-01-01T00:00:00Z')")
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Close()

	archive := filepath.Join(t.TempDir(), "newer.tar.gz")
	var out, errOut strings.Builder
	if code := backupCmd([]string{"--data", src, "--to", archive}, &out, &errOut); code != 0 {
		t.Fatalf("backup: %d %s", code, errOut.String())
	}
	errOut.Reset()
	if code := restoreCmd([]string{"--from", archive, "--data", t.TempDir()}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "newer") {
		t.Errorf("a newer schema restored: %d %q", code, errOut.String())
	}
}

// Restoring into a directory that doesn't exist yet creates it (the container's
// root is read-only, so a data path is the only place it can go).
func TestRestoreCreatesTheDataDirectory(t *testing.T) {
	src := t.TempDir()
	makeInstall(t, src)
	archive := filepath.Join(t.TempDir(), "b.tar.gz")
	var out, errOut strings.Builder
	if code := backupCmd([]string{"--data", src, "--to", archive}, &out, &errOut); code != 0 {
		t.Fatalf("backup: %d %s", code, errOut.String())
	}
	dst := filepath.Join(t.TempDir(), "new", "deeper")
	if code := restoreCmd([]string{"--from", archive, "--data", dst}, &out, &errOut); code != 0 {
		t.Fatalf("restore into %s: %d %s", dst, code, errOut.String())
	}
	if !fileExists(filepath.Join(dst, "huntdeck.db")) {
		t.Errorf("nothing restored into %s", dst)
	}
}

// --dry-run checks an archive without writing anything, even over an existing
// install.
func TestRestoreDryRun(t *testing.T) {
	src := t.TempDir()
	makeInstall(t, src)
	archive := filepath.Join(t.TempDir(), "b.tar.gz")
	var out, errOut strings.Builder
	if code := backupCmd([]string{"--data", src, "--to", archive}, &out, &errOut); code != 0 {
		t.Fatalf("backup: %d %s", code, errOut.String())
	}
	dst := t.TempDir()
	out.Reset()
	if code := restoreCmd([]string{"--from", archive, "--data", dst, "--dry-run"}, &out, &errOut); code != 0 {
		t.Fatalf("dry run: %d %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "nothing written") {
		t.Errorf("the dry run said %q", out.String())
	}
	if fileExists(filepath.Join(dst, "huntdeck.db")) {
		t.Error("the dry run wrote a database")
	}
}

// --keep leaves only the newest backups beside the new one.
func TestBackupKeep(t *testing.T) {
	src := t.TempDir()
	makeInstall(t, src)
	dir := t.TempDir()
	var out, errOut strings.Builder
	for i := 0; i < 3; i++ {
		name := filepath.Join(dir, "huntdeck-backup-old"+strconv.Itoa(i)+".tar.gz")
		if code := backupCmd([]string{"--data", src, "--to", name}, &out, &errOut); code != 0 {
			t.Fatalf("backup %d: %d %s", i, code, errOut.String())
		}
	}
	if code := backupCmd([]string{"--data", src, "--to", filepath.Join(dir, "huntdeck-backup-new.tar.gz"), "--keep", "2"}, &out, &errOut); code != 0 {
		t.Fatalf("backup --keep: %d %s", code, errOut.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("%d backups left: %v", len(entries), names)
	}
}

// A backup whose manifest disagrees with the database inside it is damaged,
// and restore says so instead of writing it over a working install.
func TestRestoreRefusesDamagedArchive(t *testing.T) {
	src := t.TempDir()
	makeInstall(t, src)
	archive := filepath.Join(t.TempDir(), "b.tar.gz")
	var out, errOut strings.Builder
	if code := backupCmd([]string{"--data", src, "--to", archive}, &out, &errOut); code != 0 {
		t.Fatalf("backup: %d %s", code, errOut.String())
	}
	// Rewrite the manifest with a count the database cannot match.
	tampered := filepath.Join(t.TempDir(), "t.tar.gz")
	rewriteManifest(t, archive, tampered, []byte(`{"programs": 999999}`))

	errOut.Reset()
	code := restoreCmd([]string{"--from", tampered, "--data", t.TempDir()}, &out, &errOut)
	if code == 0 || !strings.Contains(errOut.String(), "damaged") {
		t.Errorf("a tampered archive restored: %d %q", code, errOut.String())
	}
}

// rewriteManifest copies a backup, replacing its manifest.json.
func rewriteManifest(t *testing.T, from, to string, manifest []byte) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	gzIn, err := gzip.NewReader(in)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gzIn)

	f, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gzOut := gzip.NewWriter(f)
	tw := tar.NewWriter(gzOut)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Name == "manifest.json" {
			body = manifest
		}
		hdr.Size = int64(len(body))
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzOut.Close(); err != nil {
		t.Fatal(err)
	}
}
