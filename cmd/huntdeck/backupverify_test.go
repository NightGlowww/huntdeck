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

// A backup that cannot be read is worse than none, because it is believed: the
// verifier has to say so, not pass.
func TestBackupVerifierRefusesWhatItCannotRead(t *testing.T) {
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
	if _, err := d.Migrate(ctx, migs, dir, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()

	archive := filepath.Join(dir, "good.tar.gz")
	var out, errOut bytes.Buffer
	if code := backupCmd([]string{"-data", dir, "-to", archive}, &out, &errOut); code != 0 {
		t.Fatalf("backup failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "verified: database v") {
		t.Errorf("no verification line: %s", out.String())
	}

	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, data[:len(data)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyBackup(archive); err == nil {
		t.Error("a truncated archive verified")
	}
}
