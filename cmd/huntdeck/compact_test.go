package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/db"
)

// A dry run reports without writing, and compact refuses while a server holds
// the data directory: the same lock that protects restore.
func TestCompactDryRunAndLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huntdeck.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Migrate(ctx, migs, dir, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()

	var out, errOut bytes.Buffer
	if code := compactCmd([]string{"-data", dir, "-dry-run"}, &out, &errOut); code != 0 {
		t.Fatalf("dry run failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "nothing written") {
		t.Errorf("dry run output: %q", out.String())
	}

	release, err := lockData(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	out.Reset()
	errOut.Reset()
	if code := compactCmd([]string{"-data", dir}, &out, &errOut); code == 0 {
		t.Errorf("compact ran while the directory was locked")
	}
}
