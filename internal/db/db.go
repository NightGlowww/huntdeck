// Package db opens the HuntDeck SQLite database and applies migrations.
//
// SQLite allows one writer at a time, so DB keeps two pools: a single
// connection for writes (transactions start with BEGIN IMMEDIATE, avoiding
// SQLITE_BUSY lock upgrades) and a read-only pool for concurrent readers.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"runtime"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// DB holds the writer and reader connection pools for one database file.
type DB struct {
	W *sql.DB // single connection; use for every write
	R *sql.DB // read-only pool
}

// Open opens (creating if needed) the database at path.
func Open(ctx context.Context, path string) (*DB, error) {
	common := []string{
		"busy_timeout(5000)",
		"foreign_keys(ON)",
		"synchronous(NORMAL)",
		"temp_store(MEMORY)", // keeps the root filesystem read-only in Docker
		// SQLite reuses the WAL file rather than shrinking it, so one big poll leaves
		// its high-water mark on disk forever: 45 MiB beside a 178 MiB database here.
		// This drops it back to 16 MiB after a checkpoint.
		"journal_size_limit(16777216)",
	}

	w, err := sql.Open("sqlite", dsn(path, append([]string{"journal_mode(WAL)"}, common...), true))
	if err != nil {
		return nil, fmt.Errorf("open writer: %w", err)
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	w.SetConnMaxIdleTime(0)
	// Connect the writer first so the file exists and is in WAL mode before
	// any reader opens it.
	if err := w.PingContext(ctx); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// It holds private programs' data: its owner's alone, also where an
	// older version (or another umask) left it readable to others.
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(f, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
			_ = w.Close()
			return nil, fmt.Errorf("open %s: make it private: %w", path, err)
		}
	}

	r, err := sql.Open("sqlite", dsn(path, append(common, "query_only(ON)"), false))
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("open reader: %w", err)
	}
	r.SetMaxOpenConns(max(4, runtime.NumCPU()))
	if err := r.PingContext(ctx); err != nil {
		_ = w.Close()
		_ = r.Close()
		return nil, fmt.Errorf("open %s for reading: %w", path, err)
	}

	return &DB{W: w, R: r}, nil
}

// Ping checks that both pools can reach the database.
func (d *DB) Ping(ctx context.Context) error {
	if err := d.W.PingContext(ctx); err != nil {
		return fmt.Errorf("writer: %w", err)
	}
	if err := d.R.PingContext(ctx); err != nil {
		return fmt.Errorf("reader: %w", err)
	}
	return nil
}

// Close closes both pools.
func (d *DB) Close() error {
	rerr := d.R.Close()
	if err := d.W.Close(); err != nil {
		return err
	}
	return rerr
}

func dsn(path string, pragmas []string, immediate bool) string {
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	if immediate {
		q.Set("_txlock", "immediate")
	}
	return "file:" + (&url.URL{Path: path}).EscapedPath() + "?" + q.Encode()
}
