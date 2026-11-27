package overrides

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/secretbox"
	"github.com/mdpsec/huntdeck/internal/store"
)

const password = "p#ss$word!-unique-7f3a"

func newSaved(t *testing.T, env map[string]string) (*Saved, *store.Store, string) {
	t.Helper()
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
	if _, err := d.Migrate(ctx, migs, t.TempDir(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	st := store.New(d, nil)
	src := config.Source{
		LookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		ReadFile:  func(string) ([]byte, error) { return nil, os.ErrNotExist },
	}
	return New(st, dir, src), st, dir
}

func ptr(s string) *string { return &s }

func field(t *testing.T, fs []FieldState, name string) FieldState {
	t.Helper()
	for _, f := range fs {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no field %s", name)
	return FieldState{}
}

func TestSavedValuesTakePrecedenceAndStayEncrypted(t *testing.T) {
	ctx := context.Background()
	s, st, dir := newSaved(t, map[string]string{"BC_PUBLIC_ONLY": "true", "YWH_EMAIL": "env@example.com", "YWH_PASSWORD": "envpw"})

	cfg, err := s.Config(ctx)
	if err != nil || cfg.Bugcrowd.Email != "" || !cfg.Bugcrowd.PublicOnly {
		t.Fatalf("before saving: %+v, %v", cfg.Bugcrowd, err)
	}
	if _, err := os.Stat(filepath.Join(dir, secretbox.KeyFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a key was made before anything was saved")
	}

	err = s.Save(ctx, map[string]*string{
		"BC_EMAIL": ptr("hunter@example.com"), "BC_PASSWORD": ptr(password), "BC_TOTP_SECRET": ptr("JBSWY3DPEHPK3PXP"),
		"BC_PUBLIC_ONLY": ptr(""), // cleared: the environment's true no longer counts
		"YWH_EMAIL":      ptr(""), "YWH_PASSWORD": ptr(""),
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = s.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if b := cfg.Bugcrowd; b.Email != "hunter@example.com" || b.Password.Reveal() != password || b.PublicOnly {
		t.Errorf("Bugcrowd after saving: email %q, password right %v, public only %v", b.Email, b.Password.Reveal() == password, b.PublicOnly)
	}
	if cfg.YesWeHack.Enabled() {
		t.Error("YesWeHack's cleared login still counts")
	}

	// The database holds none of it in the clear, and a seed has none of it.
	raw, err := st.Settings(ctx, prefix)
	if err != nil || len(raw) != 6 {
		t.Fatalf("stored rows: %d, %v", len(raw), err)
	}
	for k, v := range raw {
		if bytes.Contains([]byte(v), []byte("hunter@")) || bytes.Contains([]byte(v), []byte(password)) {
			t.Errorf("%s is stored in the clear", k)
		}
	}
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(t.TempDir(), "seed.db")
	if _, err := st.ExportSeed(ctx, seed, migs, store.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(seed)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(prefix+"BC_")) {
		t.Error("the seed holds saved settings")
	}

	// Settings never gets a secret back.
	fs, err := s.Fields(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pw := field(t, fs, "BC_PASSWORD"); pw.Value != "" || !pw.Set || pw.From != FromSaved {
		t.Errorf("BC_PASSWORD's state: %+v", pw)
	}
	if em := field(t, fs, "BC_EMAIL"); em.Value != "hunter@example.com" || em.From != FromSaved {
		t.Errorf("BC_EMAIL's state: %+v", em)
	}
	if po := field(t, fs, "BC_PUBLIC_ONLY"); po.Set || po.From != FromSaved || !po.EnvSet {
		t.Errorf("BC_PUBLIC_ONLY's state: %+v", po)
	}
	if tok := field(t, fs, "IT_TOKEN"); tok.Set || tok.From != FromNone {
		t.Errorf("IT_TOKEN's state: %+v", tok)
	}
	if aj := field(t, fs, "VB_AUTO_JOIN"); aj.Set || aj.From != FromNone || !aj.On() {
		t.Errorf("VB_AUTO_JOIN's default state: %+v (want On() == true)", aj)
	}

	// Reset goes back to the environment.
	if err := s.Save(ctx, map[string]*string{"YWH_EMAIL": nil, "YWH_PASSWORD": nil}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := s.Config(ctx); cfg.YesWeHack.Email != "env@example.com" {
		t.Errorf("after reset YWH_EMAIL = %q, want the environment's", cfg.YesWeHack.Email)
	}
}

func TestSaveRefusesAnInvalidConfiguration(t *testing.T) {
	ctx := context.Background()
	s, st, _ := newSaved(t, nil)
	for _, change := range []map[string]*string{
		{"BC_EMAIL": ptr("hunter@example.com")},      // no password
		{"BC_RATE": ptr("50")},                       // out of range
		{"HUNTDECK_PROXY": ptr("ftp://" + password)}, // not a proxy URL
	} {
		err := s.Save(ctx, change)
		var invalid ValidationError
		if !errors.As(err, &invalid) {
			t.Errorf("Save(%v) = %v, want a ValidationError", change, err)
		}
		if err != nil && bytes.Contains([]byte(err.Error()), []byte(password)) {
			t.Errorf("the error shows the value: %v", err)
		}
	}
	if err := s.Save(ctx, map[string]*string{"HUNTDECK_LISTEN": ptr(":1")}); err == nil {
		t.Error("a setting that isn't editable was saved")
	}
	if raw, _ := st.Settings(ctx, prefix); len(raw) != 0 {
		t.Errorf("refused changes were stored: %d rows", len(raw))
	}
}

// With the key file lost, saved values can't be read: the environment's
// count, Settings says which, and saving again replaces them.
func TestLostKey(t *testing.T) {
	ctx := context.Background()
	s, _, dir := newSaved(t, map[string]string{"BC_PUBLIC_ONLY": "true"})
	if err := s.Save(ctx, map[string]*string{"BC_RATE": ptr("0.5")}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, secretbox.KeyFile)); err != nil {
		t.Fatal(err)
	}
	_, unreadable, err := s.Source(ctx)
	if err != nil || len(unreadable) != 1 || unreadable[0] != "BC_RATE" {
		t.Fatalf("unreadable = %v, %v", unreadable, err)
	}
	if cfg, err := s.Config(ctx); err != nil || cfg.Bugcrowd.RatePerSecond != 1 {
		t.Errorf("rate = %v, %v; want the default", cfg.Bugcrowd.RatePerSecond, err)
	}
	fs, _ := s.Fields(ctx)
	if f := field(t, fs, "BC_RATE"); !f.Unreadable {
		t.Errorf("BC_RATE's state: %+v", f)
	}
	if err := s.Save(ctx, map[string]*string{"BC_RATE": ptr("0.7")}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := s.Config(ctx); cfg.Bugcrowd.RatePerSecond != 0.7 {
		t.Errorf("rate after saving again = %v", cfg.Bugcrowd.RatePerSecond)
	}
}

// A saved value hides the variable's NAME_FILE and aliases, rather than
// clashing with them.
func TestOverlayHidesFilesAndAliases(t *testing.T) {
	env := map[string]string{"BC_PASSWORD_FILE": "/run/secrets/bc", "BC_EMAIL": "a@example.com", "H1_OTP": "JBSWY3DPEHPK3PXP"}
	src := config.Source{
		LookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		ReadFile:  func(string) ([]byte, error) { return []byte("from-file\n"), nil },
	}
	if v, ok := src.Lookup(mustField(t, "BC_PASSWORD")); !ok || v != "from-file" {
		t.Errorf("Lookup through the file = %q, %v", v, ok)
	}
	if v, ok := src.Lookup(mustField(t, "H1_TOTP_SECRET")); !ok || v != "JBSWY3DPEHPK3PXP" {
		t.Errorf("Lookup through the alias = %q, %v", v, ok)
	}
	cfg, err := config.Load(src.Overlay(map[string]string{"BC_PASSWORD": "saved", "H1_TOTP_SECRET": ""}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bugcrowd.Password.Reveal() != "saved" || cfg.HackerOne.TOTPSecret != "" {
		t.Errorf("password %v, H1 TOTP set %v", cfg.Bugcrowd.Password.Reveal() == "saved", cfg.HackerOne.TOTPSecret != "")
	}
}

func mustField(t *testing.T, name string) config.Field {
	t.Helper()
	f, ok := config.EditableField(name)
	if !ok {
		t.Fatalf("%s isn't editable", name)
	}
	return f
}
