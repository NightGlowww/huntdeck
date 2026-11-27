package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// A read-only copy's platforms are its seed's, in the usual order, with
// disclosed reports where it has some.
func TestHeldPlatforms(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := db.Open(ctx, filepath.Join(dir, "huntdeck.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Migrate(ctx, migs, filepath.Join(dir, "backups"), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	st := store.New(d, nil)
	err = st.Write(ctx, func(tx *store.Tx) error {
		for _, p := range []string{"inspectiv", "vulbox", "gobugfree", "yeswehack", "zz-other", "issuehunt", "hackerone", "bugcrowd"} {
			id, err := tx.UpsertProgram(ctx, p, platform.Program{Handle: "acme", Name: "Acme", URL: "https://example.com/acme", State: "open"})
			if err != nil {
				return err
			}
			if p == "hackerone" {
				if _, err := tx.SaveDisclosure(ctx, id, platform.Disclosure{ID: "1", ProgramHandle: "acme", Title: "x",
					DisclosedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), URL: "https://example.com/r/1"}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := heldPlatforms(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range got {
		names = append(names, p.Name)
		if p.Discloses != (p.Name == "hackerone") {
			t.Errorf("%s discloses %v", p.Name, p.Discloses)
		}
	}
	if want := "hackerone bugcrowd yeswehack vulbox issuehunt gobugfree inspectiv zz-other"; fmt.Sprint(names) != "["+want+"]" {
		t.Errorf("platforms %v, want %s", names, want)
	}
}
