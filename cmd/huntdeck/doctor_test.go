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

// doctor says a healthy data directory is healthy, and names the problems it
// finds in a broken one.
func TestDoctorCmd(t *testing.T) {
	dir := t.TempDir()
	makeInstall(t, dir)
	var out, errOut strings.Builder
	if code := doctorCmd([]string{"--data", dir}, &out, &errOut); code != 0 {
		t.Fatalf("doctor on a healthy install: %d\n%s%s", code, out.String(), errOut.String())
	}
	for _, want := range []string{"is writable", "up to date", "secret.key is present", "free of"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the report lacks %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if code := doctorCmd([]string{"--data", filepath.Join(dir, "nope")}, &out, &errOut); code != 1 {
		t.Errorf("doctor on a missing directory = %d", code)
	}
	if !strings.Contains(out.String(), "there is no") {
		t.Errorf("the report doesn't name the missing directory:\n%s", out.String())
	}

	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(dir, "huntdeck.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.W.ExecContext(ctx, "INSERT INTO schema_migrations (version, name, applied_at) VALUES (999, 'future', '2026-01-01T00:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	out.Reset()
	if code := doctorCmd([]string{"--data", dir}, &out, &errOut); code != 1 || !strings.Contains(out.String(), "newer") {
		t.Errorf("doctor on a newer schema = %d:\n%s", code, out.String())
	}
}

// A corrupt database is reported, not blessed: doctor is the first thing run
// when something is off, so it must not say "no problems found" over damage.
func TestDoctorReportsCorruption(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "huntdeck.db")
	ctx := context.Background()
	d, err := db.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	if _, err := d.Migrate(ctx, migs, dir, log); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()

	// Corrupt a page in the middle of the file.
	f, err := os.OpenFile(dbPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("not a sqlite page at all"), 4096); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	var out, errOut bytes.Buffer
	code := doctorCmd([]string{"-data", dir}, &out, &errOut)
	if code == 0 {
		t.Errorf("doctor passed a corrupted database:\n%s", out.String())
	}
}
