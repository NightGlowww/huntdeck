package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/db"
)

// Only a signed-out seed's database, holding nothing private or of an
// account's, passes the read-only check.
func TestCheckSignedOut(t *testing.T) {
	ctx := context.Background()
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	src := newTestStore(t, nil)
	exec(t, src, `
		INSERT INTO programs (id, platform, handle, name, url, private, state, offers_bounty, raw, first_seen_at, last_seen_at)
		VALUES (1, 'hackerone', 'open', 'O', 'https://example.com/o', 0, 'open', 1, '{}', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
			(2, 'hackerone', 'secret', 'S', 'https://example.com/s', 1, 'open', 1, '{}', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z');`)
	load := func(o SeedOptions) *Store {
		path := filepath.Join(t.TempDir(), "seed.db")
		if _, err := src.ExportSeed(ctx, path, migs, o); err != nil {
			t.Fatal(err)
		}
		dst := newTestStore(t, nil)
		if _, err := dst.ImportSeed(ctx, path, t.TempDir(), migs); err != nil {
			t.Fatal(err)
		}
		return dst
	}
	for _, tt := range []struct {
		name string
		st   *Store
		fix  string // SQL run on it first
		want string // "" passes, else part of the error
	}{
		{"the full install", src, "", "load one exported with"},
		{"a seed without --signed-out", load(SeedOptions{}), "", "load one exported with"},
		{"a signed-out seed", load(SeedOptions{SignedOut: true}), "", ""},
		{"with a private program", load(SeedOptions{SignedOut: true}), `UPDATE programs SET private = 1`, "private, invited or joined"},
		{"with a tag", load(SeedOptions{SignedOut: true}), `INSERT INTO program_tags (program_id, tag) VALUES (1, 'x')`, "tags"},
		{"with one of the user's reports", load(SeedOptions{SignedOut: true}), `INSERT INTO my_reports (platform, report_key, state,
			submitted_at, first_seen_at, updated_at) VALUES ('hackerone', '1', 'new', '2026-01-01T00:00:00.000Z', '', '')`, "own reports"},
		{"with a saved account setting", load(SeedOptions{SignedOut: true}), `INSERT INTO settings (key, value, updated_at) VALUES ('accounts.H1_TOKEN', 'x', '')`, "settings"},
	} {
		if tt.fix != "" {
			exec(t, tt.st, tt.fix)
		}
		err := tt.st.CheckSignedOut(ctx)
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("%s: %v", tt.name, err)
		case tt.want != "" && (!errors.Is(err, ErrNotSignedOut) || !strings.Contains(err.Error(), tt.want)):
			t.Errorf("%s: %v, want %q", tt.name, err, tt.want)
		}
	}
}
