package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The user's HuntMail addresses, by program. The mail stays in HuntMail;
// HuntDeck remembers each program's prefix and the addresses made with
// it, so the program's mail is everything sent to "<prefix>-…".

// MaxMailPrefix is how long a program's prefix may be before its number.
const MaxMailPrefix = 20

// MaxMailNote is how long an address's note may be.
const MaxMailNote = 120

// ErrMailAddressTaken: the address is already one of the user's.
var ErrMailAddressTaken = errors.New("that address is already saved")

// MailAddress is an address HuntDeck made (or the user saved) for a
// program.
type MailAddress struct {
	ID        int64
	ProgramID int64
	Address   string
	Note      string
	CreatedAt time.Time
}

// MailPrefix is a program's prefix, with what a list of mail needs to name
// the program.
type MailPrefix struct {
	ProgramID int64
	Prefix    string
	Platform  string
	Handle    string
	Name      string
}

// MailSlug turns a program's name into a prefix: lowercase letters and
// digits only, at most MaxMailPrefix of them, "program" when nothing is
// left. No dash, so "<prefix>-" can never match another program's.
func MailSlug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			if b.Len() == MaxMailPrefix {
				break
			}
		}
	}
	if b.Len() < 3 { // HuntMail's prefixes are 3 characters or more, with the dash
		return "program"
	}
	return b.String()
}

// MailPrefixOf returns a program's prefix, "" when it has none yet.
func (s *Store) MailPrefixOf(ctx context.Context, programID int64) (string, error) {
	var p string
	err := s.db.R.QueryRowContext(ctx, `SELECT prefix FROM mail_prefixes WHERE program_id = ?`, programID).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return p, err
}

// EnsureMailPrefix returns a program's prefix, giving it one from slug
// (MailSlug) the first time: the slug itself if it's free, else the slug
// with the lowest number that is (acme2, acme3, …).
func (s *Store) EnsureMailPrefix(ctx context.Context, programID int64, slug string) (string, error) {
	var out string
	err := s.Write(ctx, func(tx *Tx) error {
		err := tx.tx.QueryRowContext(ctx, `SELECT prefix FROM mail_prefixes WHERE program_id = ?`, programID).Scan(&out)
		if err == nil || !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		for n := 1; n < 10000; n++ {
			try := slug
			if n > 1 {
				try = slug + strconv.Itoa(n)
			}
			res, err := tx.tx.ExecContext(ctx, `INSERT INTO mail_prefixes (program_id, prefix, created_at) VALUES (?, ?, ?)
				ON CONFLICT (prefix) DO NOTHING`, programID, try, tx.now)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 1 {
				out = try
				return nil
			}
		}
		return fmt.Errorf("no free mail prefix for %q", slug)
	})
	return out, err
}

// MailPrefixes returns every program's prefix, longest first, so the first
// one a recipient starts with (plus its dash) is its program.
func (s *Store) MailPrefixes(ctx context.Context) ([]MailPrefix, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT m.program_id, m.prefix, p.platform, p.handle, p.name FROM mail_prefixes m JOIN programs p ON p.id = m.program_id
		ORDER BY length(m.prefix) DESC, m.prefix`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []MailPrefix
	for rows.Next() {
		var m MailPrefix
		if err := rows.Scan(&m.ProgramID, &m.Prefix, &m.Platform, &m.Handle, &m.Name); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddMailAddress saves an address for a program.
func (s *Store) AddMailAddress(ctx context.Context, programID int64, address, note string) (MailAddress, error) {
	a := MailAddress{ProgramID: programID, Address: strings.ToLower(strings.TrimSpace(address)), Note: trimNote(note)}
	err := s.Write(ctx, func(tx *Tx) error {
		res, err := tx.tx.ExecContext(ctx, `INSERT INTO mail_addresses (program_id, address, note, created_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (address) DO NOTHING`, programID, a.Address, a.Note, tx.now)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrMailAddressTaken
		}
		a.ID, _ = res.LastInsertId()
		a.CreatedAt = parseTS(tx.now)
		return nil
	})
	return a, err
}

// MailAddresses returns a program's addresses, newest first.
func (s *Store) MailAddresses(ctx context.Context, programID int64) ([]MailAddress, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, program_id, address, note, created_at FROM mail_addresses
		WHERE program_id = ? ORDER BY created_at DESC, id DESC`, programID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []MailAddress
	for rows.Next() {
		var a MailAddress
		var created string
		if err := rows.Scan(&a.ID, &a.ProgramID, &a.Address, &a.Note, &created); err != nil {
			return nil, err
		}
		a.CreatedAt = parseTS(created)
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetMailAddressNote changes what an address of programID is for.
func (s *Store) SetMailAddressNote(ctx context.Context, programID, id int64, note string) error {
	return s.Write(ctx, func(tx *Tx) error {
		res, err := tx.tx.ExecContext(ctx, `UPDATE mail_addresses SET note = ? WHERE id = ? AND program_id = ?`, trimNote(note), id, programID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// DeleteMailAddress forgets an address of programID. Its mail stays in
// HuntMail, and still shows under the program while it has the prefix.
func (s *Store) DeleteMailAddress(ctx context.Context, programID, id int64) error {
	return s.Write(ctx, func(tx *Tx) error {
		res, err := tx.tx.ExecContext(ctx, `DELETE FROM mail_addresses WHERE id = ? AND program_id = ?`, id, programID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func trimNote(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > MaxMailNote {
		s = strings.ToValidUTF8(s[:MaxMailNote], "")
	}
	return s
}

// ProgramsUnder returns, for each root domain (scope.RootDomain), the
// programs whose live in-scope targets are under it: private ones (invites)
// first, then by name. The Email page's guess at the program a sender is.
func (s *Store) ProgramsUnder(ctx context.Context, apexes []string) (map[string][]ProgramRef, error) {
	out := map[string][]ProgramRef{}
	if len(apexes) == 0 {
		return out, nil
	}
	args := make([]any, len(apexes))
	for i, a := range apexes {
		args[i] = a
	}
	//nolint:gosec // only placeholders are concatenated; values are bound
	query := `
		SELECT DISTINCT t.apex, p.id, p.platform, p.handle, p.name, p.url, p.private, p.state
		FROM scope_targets t
		JOIN scope_entries e ON e.id = t.scope_entry_id
		JOIN programs p ON p.id = e.program_id
		WHERE e.removed_at IS NULL AND p.removed_at IS NULL AND e.in_scope = 1 AND t.apex IN (` + placeholders(len(apexes)) + `)
		ORDER BY p.private DESC, p.name COLLATE NOCASE, p.id`
	rows, err := s.db.R.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var apex string
		var p ProgramRef
		if err := rows.Scan(&apex, &p.ID, &p.Platform, &p.Handle, &p.Name, &p.URL, &p.Private, &p.State); err != nil {
			return nil, err
		}
		out[apex] = append(out[apex], p)
	}
	return out, rows.Err()
}

// ProgramKeys are the live programs by the words an address or a sender's
// domain would name them with: each one's name and its handle, as MailSlug
// writes them (letters and digits, lowercased), private first, then by
// name.
func (s *Store) ProgramKeys(ctx context.Context) (map[string][]ProgramRef, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT id, platform, handle, name, url, private, state FROM programs
		WHERE removed_at IS NULL ORDER BY private DESC, name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]ProgramRef{}
	for rows.Next() {
		var p ProgramRef
		if err := rows.Scan(&p.ID, &p.Platform, &p.Handle, &p.Name, &p.URL, &p.Private, &p.State); err != nil {
			return nil, err
		}
		name, handle := MailSlug(p.Name), MailSlug(p.Handle)
		for _, k := range []string{name, handle} {
			if k != "program" && !slices.ContainsFunc(out[k], func(q ProgramRef) bool { return q.ID == p.ID }) {
				out[k] = append(out[k], p)
			}
		}
	}
	return out, rows.Err()
}
