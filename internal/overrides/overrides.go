// Package overrides keeps the settings entered in Settings > Accounts
// that would otherwise come from the environment (.env): platform
// credentials, request rates, the proxy. A saved value takes precedence
// over the environment's until it's reset.
//
// Values are stored in the settings table as "env.<NAME>", every one
// encrypted (internal/secretbox) with the key in the data directory, and
// seeds never copy them (their settings are an allow-list). Secrets are
// write-only: nothing here returns one to the UI.
package overrides

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/secretbox"
	"github.com/mdpsec/huntdeck/internal/store"
)

// prefix starts every saved value's settings key.
const prefix = "env."

// Saved reads and writes the saved values.
type Saved struct {
	store   *store.Store
	keyPath string
	env     config.Source
}

// New returns the saved values of the install whose data directory is
// dataDir, over the environment env.
func New(st *store.Store, dataDir string, env config.Source) *Saved {
	return &Saved{store: st, keyPath: filepath.Join(dataDir, secretbox.KeyFile), env: env}
}

// values returns the saved values that can be read, by variable name, and
// the names of those that can't (the key file was lost or replaced).
func (s *Saved) values(ctx context.Context) (map[string]string, []string, error) {
	rows, err := s.store.Settings(ctx, prefix)
	if err != nil {
		return nil, nil, fmt.Errorf("saved settings: %w", err)
	}
	vals := map[string]string{}
	if len(rows) == 0 {
		return vals, nil, nil
	}
	box, err := secretbox.Load(s.keyPath)
	if err != nil && !errors.Is(err, secretbox.ErrNoKey) {
		return nil, nil, fmt.Errorf("saved settings: %w", err)
	}
	var unreadable []string
	for key, sealed := range rows {
		name := strings.TrimPrefix(key, prefix)
		if _, ok := config.EditableField(name); !ok {
			continue // no longer editable: the environment's counts
		}
		if box == nil {
			unreadable = append(unreadable, name)
			continue
		}
		v, err := box.Open(name, sealed)
		if err != nil {
			unreadable = append(unreadable, name)
			continue
		}
		vals[name] = v
	}
	slices.Sort(unreadable)
	return vals, unreadable, nil
}

// Source returns the environment with the saved values over it, and the
// names of saved values that couldn't be read (left to the environment).
func (s *Saved) Source(ctx context.Context) (config.Source, []string, error) {
	vals, unreadable, err := s.values(ctx)
	if err != nil {
		return config.Source{}, nil, err
	}
	return s.env.Overlay(vals), unreadable, nil
}

// Config loads the configuration from the environment and the saved values,
// then checks what only the joined picture can answer: a TOTP secret is often in
// the environment while the email and password it belongs to are saved here, so
// the cross-field rules run on the result, not on either source alone.
func (s *Saved) Config(ctx context.Context) (config.Config, error) {
	src, _, err := s.Source(ctx)
	if err != nil {
		return config.Config{}, err
	}
	cfg, err := config.Load(src)
	if err != nil {
		return config.Config{}, err
	}
	if err := config.Check(cfg); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

// Where a field's value comes from.
const (
	FromSaved = "saved" // saved in Settings (maybe "", cleared)
	FromEnv   = "env"   // the environment, .env or a NAME_FILE
	FromNone  = ""      // not set
)

// FieldState is an editable field as Settings shows it. Value is empty
// for secrets.
type FieldState struct {
	config.Field
	Value      string // the value in force; never a secret's
	Set        bool   // there's a value in force
	From       string // FromSaved, FromEnv or FromNone
	EnvSet     bool   // the environment sets it too
	Unreadable bool   // saved, but it can't be decrypted
}

// Fields returns every editable field's state.
func (s *Saved) Fields(ctx context.Context) ([]FieldState, error) {
	vals, unreadable, err := s.values(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]FieldState, 0, len(config.Editable))
	for _, f := range config.Editable {
		st := FieldState{Field: f, Unreadable: slices.Contains(unreadable, f.Name)}
		envVal, envSet := s.env.Lookup(f)
		st.EnvSet = envSet
		v, saved := vals[f.Name]
		switch {
		case saved:
			st.From, st.Set = FromSaved, v != ""
		case envSet:
			st.From, st.Set, v = FromEnv, true, envVal
		}
		if !f.Secret() {
			st.Value = v
		}
		out = append(out, st)
	}
	return out, nil
}

// On reports whether a switch is on, falling back to its default when unset.
func (f FieldState) On() bool {
	v := f.Value
	if v == "" {
		v = f.Default
	}
	on, _ := strconv.ParseBool(v)
	return on
}

// ValidationError is a configuration the change would make invalid; its
// text names variables, never a secret's value.
type ValidationError struct{ Err error }

func (e ValidationError) Error() string { return e.Err.Error() }

// Save applies changes, by variable name: a value to save (an empty one
// clears it, whatever the environment says), or nil to drop the saved
// value and go back to the environment's. Nothing is saved if the result
// wouldn't load (a ValidationError) or a name isn't editable.
func (s *Saved) Save(ctx context.Context, changes map[string]*string) error {
	if len(changes) == 0 {
		return nil
	}
	vals, _, err := s.values(ctx)
	if err != nil {
		return err
	}
	for name, v := range changes {
		if _, ok := config.EditableField(name); !ok {
			return fmt.Errorf("saved settings: %s can't be set in Settings", name)
		}
		if v == nil {
			delete(vals, name)
		} else {
			vals[name] = *v
		}
	}
	if cfg, err := config.Load(s.env.Overlay(vals)); err != nil {
		return ValidationError{err}
	} else if err := config.Check(cfg); err != nil {
		return ValidationError{err}
	}
	box, err := secretbox.LoadOrCreate(s.keyPath)
	if err != nil {
		return fmt.Errorf("saved settings: %w", err)
	}
	sealed := map[string]string{}
	for name, v := range changes {
		if v != nil {
			if sealed[name], err = box.Seal(name, *v); err != nil {
				return err
			}
		}
	}
	return s.store.Write(ctx, func(tx *store.Tx) error {
		for name, v := range changes {
			var err error
			if v == nil {
				err = tx.DeleteSetting(ctx, prefix+name)
			} else {
				err = tx.PutSetting(ctx, prefix+name, sealed[name])
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// pollPrefix marks platforms to poll once the server restarts with their
// new credentials, so Settings shows straight away whether they work.
const pollPrefix = "accounts.poll."

// PollAfterRestart asks for the platform to be polled after the restart.
func (s *Saved) PollAfterRestart(ctx context.Context, platformName string) error {
	return s.store.Write(ctx, func(tx *store.Tx) error { return tx.PutSetting(ctx, pollPrefix+platformName, "1") })
}

// TakePollAfterRestart returns the platforms to poll now, forgetting them.
func (s *Saved) TakePollAfterRestart(ctx context.Context) ([]string, error) {
	rows, err := s.store.Settings(ctx, pollPrefix)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	var names []string
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		names = names[:0]
		for key := range rows {
			names = append(names, strings.TrimPrefix(key, pollPrefix))
			if err := tx.DeleteSetting(ctx, key); err != nil {
				return err
			}
		}
		return nil
	})
	slices.Sort(names)
	return names, err
}
