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
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/db"
)

// backup writes the database (a consistent snapshot), the settings key and the
// sessions, and leaves the live files alone.
func TestBackupCmd(t *testing.T) {
	dir := t.TempDir()
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

	out := filepath.Join(t.TempDir(), "b.tar.gz")
	var stdout, stderr strings.Builder
	if code := backupCmd([]string{"--data", dir, "--to", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("backup: %d %s", code, stderr.String())
	}

	names := map[string]bool{}
	var dbBytes int
	var manifest []byte
	readTar(t, out, func(name string, body []byte) {
		names[name] = true
		switch name {
		case "huntdeck.db":
			dbBytes = len(body)
		case "manifest.json":
			manifest = body
		}
	})
	for _, want := range []string{"manifest.json", "huntdeck.db", "secret.key", "sessions/hackerone/cookies.json"} {
		if !names[want] {
			t.Errorf("the backup lacks %q (has %v)", want, names)
		}
	}
	if dbBytes < 4096 {
		t.Errorf("the database snapshot is %d bytes", dbBytes)
	}
	// The manifest claims what the snapshot holds, history included.
	for _, key := range []string{"programs", "scope_entries", "changes", "poll_runs"} {
		if !strings.Contains(string(manifest), `"`+key+`"`) {
			t.Errorf("the manifest lacks %q: %s", key, manifest)
		}
	}

	// --no-sessions leaves the logins out.
	out2 := filepath.Join(t.TempDir(), "b2.tar.gz")
	if code := backupCmd([]string{"--data", dir, "--to", out2, "--no-sessions"}, &stdout, &stderr); code != 0 {
		t.Fatalf("backup --no-sessions: %d %s", code, stderr.String())
	}
	readTar(t, out2, func(name string, _ []byte) {
		if strings.HasPrefix(name, "sessions/") {
			t.Errorf("--no-sessions still wrote %q", name)
		}
	})
}

// readTar calls fn for every regular file in the backup.
func readTar(t *testing.T, path string, fn func(name string, body []byte)) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		fn(hdr.Name, body)
	}
}
