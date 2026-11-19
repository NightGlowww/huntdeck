package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mdpsec/huntdeck/internal/db"
)

// compactCmd returns the space a database has inside it to the filesystem.
// Deleted rows leave their pages behind, the file only grows, and a full disk
// is the failure that looks like everything else; this is the tool the doctor
// points at.
func compactCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("compact", flag.ContinueOnError)
	fs.SetOutput(stderr)
	data := fs.String("data", "", "data directory (default $HUNTDECK_DATA_DIR, else ./data)")
	dryRun := fs.Bool("dry-run", false, "report what would be reclaimed, change nothing")
	if err := fs.Parse(args); err != nil {
		if len(args) > 0 && args[0] == "-h" {
			return 0
		}
		return 2
	}
	dir := *data
	if dir == "" {
		dir = os.Getenv("HUNTDECK_DATA_DIR")
	}
	if dir == "" {
		dir = "data"
	}
	path := filepath.Join(dir, "huntdeck.db")
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(stderr, "huntdeck compact: no database at %s\n", path)
		return 1
	}

	// A server holds the data directory for its lifetime; VACUUM needs it to
	// itself.
	release, err := lockData(dir)
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck compact: %v\n", err)
		return 1
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	d, err := db.Open(ctx, path)
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck compact: %v\n", err)
		return 1
	}
	defer func() { _ = d.Close() }()

	before, err := dbFileStats(ctx, d)
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck compact: %v\n", err)
		return 1
	}
	if *dryRun {
		fmt.Fprintf(stdout, "%s: %s used, %s reclaimable; nothing written\n",
			path, humanBytes(before.used), humanBytes(before.free))
		return 0
	}
	if _, err := d.W.ExecContext(ctx, "VACUUM"); err != nil {
		fmt.Fprintf(stderr, "huntdeck compact: vacuum: %v\n", err)
		return 1
	}
	// optimize updates the query planner's statistics from what it has seen;
	// it is cheap and it is what keeps a big table's plans sane.
	if _, err := d.W.ExecContext(ctx, "PRAGMA optimize"); err != nil {
		fmt.Fprintf(stderr, "huntdeck compact: optimize: %v\n", err)
	}
	after, err := dbFileStats(ctx, d)
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck compact: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: %s used, %s free space inside the file (was %s)\n",
		path, humanBytes(after.used), humanBytes(after.free), humanBytes(before.free))
	return 0
}

type fileStats struct {
	used, free int64
}

// dbFileStats reads the database's page accounting.
func dbFileStats(ctx context.Context, d *db.DB) (fileStats, error) {
	var pages, free, size int64
	for _, q := range []struct {
		sql string
		dst *int64
	}{
		{"PRAGMA page_count", &pages},
		{"PRAGMA freelist_count", &free},
		{"PRAGMA page_size", &size},
	} {
		if err := d.R.QueryRowContext(ctx, q.sql).Scan(q.dst); err != nil {
			return fileStats{}, fmt.Errorf("%s: %w", q.sql, err)
		}
	}
	if size <= 0 {
		return fileStats{}, errors.New("page size is unknown")
	}
	return fileStats{used: (pages - free) * size, free: free * size}, nil
}
