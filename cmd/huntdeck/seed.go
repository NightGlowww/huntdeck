package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/huntscore"
	"github.com/mdpsec/huntdeck/internal/seedfetch"
	"github.com/mdpsec/huntdeck/internal/store"
	"github.com/mdpsec/huntdeck/internal/version"
)

// seedNames are the files in the data directory a new install loads its
// seed from, if one is there (see autoSeed).
var seedNames = []string{"seed.db.gz", "seed.db"}

func seedCmd(args []string, stdout, stderr io.Writer) int {
	var o store.SeedOptions
	for _, a := range args {
		if a == "-h" || a == "-help" || a == "--help" {
			args = nil // the usage below, not a file named -h
		}
	}
	if len(args) == 3 && args[0] == "export" && args[1] == "--signed-out" {
		o.SignedOut, args = true, []string{args[0], args[2]}
	}
	if len(args) != 2 || (args[0] != "export" && args[0] != "import") {
		fmt.Fprintln(stderr, `usage: huntdeck seed export [--signed-out] FILE   write the public programs' data to FILE (gzipped if it ends in .gz);
                                                 --signed-out keeps only what anyone sees without signing in, for a demo
       huntdeck seed import FILE                 load FILE into a new, empty install`)
		return 2
	}
	ctx, stop := signalContext()
	defer stop()
	app, ok := openApp(ctx, stderr, true)
	if !ok {
		return 1
	}
	defer app.close()
	migs, err := db.Migrations()
	if err != nil {
		fmt.Fprintf(stderr, "seed: %v\n", err)
		return 1
	}
	var n store.SeedCounts
	if args[0] == "export" {
		n, err = app.store.ExportSeed(ctx, args[1], migs, o)
	} else if n, err = app.store.ImportSeed(ctx, args[1], app.cfg.DataDir, migs); err == nil {
		err = huntscore.Refresh(ctx, app.store, app.store.Now()) // scores aren't in seeds
	}
	if err != nil {
		fmt.Fprintf(stderr, "seed %s: %v\n", args[0], err)
		return 1
	}
	fmt.Fprintf(stdout, "seed %s %s: %s\n", args[0], args[1], n)
	return 0
}

// autoSeed loads seed.db(.gz) from the data directory into a new install,
// then renames it so it's loaded once. A database that holds programs
// already is left alone. With no seed file there and seedURL set, a new
// install's empty database downloads the starter seed from it first
// (downloadSeed); one that can't be downloaded or loaded is skipped with a
// warning, and the install starts empty.
func autoSeed(ctx context.Context, st *store.Store, dataDir, seedURL string, client *http.Client, log *slog.Logger) error {
	downloaded := false
	if seedURL != "" && !slices.ContainsFunc(seedNames, func(n string) bool {
		_, err := os.Stat(filepath.Join(dataDir, n))
		return err == nil
	}) {
		downloaded = downloadSeed(ctx, st, dataDir, seedURL, client, log)
	}
	for _, name := range seedNames {
		path := filepath.Join(dataDir, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		migs, err := db.Migrations()
		if err != nil {
			return err
		}
		n, err := st.ImportSeed(ctx, path, dataDir, migs)
		if errors.Is(err, store.ErrNotEmpty) {
			log.Warn("seed file not loaded: the database already holds programs", "path", path)
			return nil
		}
		if err != nil && downloaded {
			_ = os.Remove(path)
			log.Warn("starter seed not loaded; starting empty (a newer HuntDeck may be needed for it)", "url", seedURL, "err", err)
			return nil
		}
		if err != nil {
			return fmt.Errorf("load seed %s (remove it to start without one): %w", path, err)
		}
		if err := os.Rename(path, path+".loaded"); err != nil {
			return err
		}
		// The marker only has to exist: keeping the archive itself holds 34 MiB
		// of a file that has already been imported.
		if err := os.Truncate(path+".loaded", 0); err != nil {
			log.Warn("seed marker not truncated", "path", path+".loaded", "err", err)
		}
		log.Info("loaded seed", "path", path, "rows", n.String())
		return huntscore.Refresh(ctx, st, st.Now()) // scores aren't in seeds
	}
	return nil
}

// seedDownloadTime is the most a starter seed's download may take.
const seedDownloadTime = 15 * time.Minute

// downloadSeed puts the starter seed at seedURL in dataDir as seed.db.gz,
// if the database is empty, and says whether it did. A failure is logged,
// not returned: the install starts empty and polls for itself, and once
// its first poll has saved programs it never asks again.
func downloadSeed(ctx context.Context, st *store.Store, dataDir, seedURL string, client *http.Client, log *slog.Logger) bool {
	empty, err := st.Empty(ctx)
	if err != nil || !empty {
		return false
	}
	log.Info("new install: downloading the starter seed (HUNTDECK_SEED_URL=off to start empty)", "url", seedURL)
	ctx, cancel := context.WithTimeout(ctx, seedDownloadTime)
	defer cancel()
	f := seedfetch.Fetcher{Client: client, UserAgent: "HuntDeck/" + version.Version}
	tmp, err := f.Fetch(ctx, seedURL, dataDir)
	if err == nil {
		if err = os.Rename(tmp, filepath.Join(dataDir, seedNames[0])); err != nil {
			_ = os.Remove(tmp)
		}
	}
	if err != nil {
		log.Warn("starter seed not downloaded; starting empty", "url", seedURL, "err", err)
		return false
	}
	return true
}
