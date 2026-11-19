package db

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

// A migration edited after it shipped is caught: the recorded digest stops
// matching, and the runner refuses rather than diverging silently.
func TestEditedMigrationIsRefused(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	migs := []Migration{
		{Version: 1, Name: "one", SQL: "CREATE TABLE a (x INTEGER) STRICT;"},
	}
	log := slog.New(slog.DiscardHandler)
	if n, err := d.Migrate(ctx, migs, dir, log); err != nil || n != 1 {
		t.Fatalf("first migrate: %d %v", n, err)
	}

	// Applied and unchanged: a second run is a no-op.
	if n, err := d.Migrate(ctx, migs, dir, log); err != nil || n != 0 {
		t.Fatalf("second migrate: %d %v", n, err)
	}

	// Edited after shipping: refused, naming the migration.
	edited := []Migration{{Version: 1, Name: "one", SQL: "CREATE TABLE a (x INTEGER, y TEXT) STRICT;"}}
	if _, err := d.Migrate(ctx, edited, dir, log); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("edited migration: %v", err)
	}
}
