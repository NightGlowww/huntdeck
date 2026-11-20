package db

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Seventeen pre-migration copies of a 150 MiB database were 2.3 GiB on a host
// that also runs a scanner, and nothing removed them. The newest few stay;
// the rest go, and a backup that is not one of ours is not touched.
func TestPruneBackupsKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.DiscardHandler)
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 6; i++ {
		name := filepath.Join(dir, "huntdeck-2026111"+string(rune('0'+i))+"T000000Z-schema-v00"+string(rune('0'+i))+".db")
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(name, at, at); err != nil {
			t.Fatal(err)
		}
	}
	// A file of the user's own, in the same directory.
	keep := filepath.Join(dir, "my-own-backup.db")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	pruneBackups(dir, log)

	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	ours := 0
	for _, e := range left {
		if e.Name() == "my-own-backup.db" {
			continue
		}
		ours++
	}
	if ours != keepBackups {
		t.Fatalf("%d pre-migration backups left, want %d", ours, keepBackups)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("a file that is not ours was removed")
	}
	_ = io.Discard
}
