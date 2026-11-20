package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// APIToken is a token a script presents instead of a login session. Only its
// digest is kept: the token itself is shown once, when it is created.
type APIToken struct {
	ID int64
	// Kind is api or mcp: the MCP page manages its own keys, the API page its own.
	Kind       string
	Name       string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// ErrTokenUnknown is returned by CheckAPIToken for a token that is not live.
var ErrTokenUnknown = errors.New("unknown or revoked token")

func tokenDigest(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// CreateAPIToken stores a token's digest and returns its id. The caller keeps
// the plaintext: nothing here could be replayed as the token itself.
func (s *Store) CreateAPIToken(ctx context.Context, kind, name, token string) (int64, error) {
	res, err := s.db.W.ExecContext(ctx,
		`INSERT INTO api_tokens (kind, name, digest, created_at) VALUES (?, ?, ?, ?)`,
		kind, name, tokenDigest(token), s.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("create token: %w", err)
	}
	return res.LastInsertId()
}

// APITokens lists every token, newest first, revoked ones included so the page
// can say what was retired.
func (s *Store) APITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := s.db.R.QueryContext(ctx,
		`SELECT id, kind, name, created_at, last_used_at, revoked_at FROM api_tokens ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		var created string
		var last, revoked sql.NullString
		if err := rows.Scan(&t.ID, &t.Kind, &t.Name, &created, &last, &revoked); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339, created)
		if last.Valid {
			if at, err := time.Parse(time.RFC3339, last.String); err == nil {
				t.LastUsedAt = &at
			}
		}
		if revoked.Valid {
			if at, err := time.Parse(time.RFC3339, revoked.String); err == nil {
				t.RevokedAt = &at
			}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CheckAPIToken reports whether token is live and records the use. Digests are
// compared in constant time, so a wrong token costs what a right one does.
func (s *Store) CheckAPIToken(ctx context.Context, token string) error {
	want := tokenDigest(token)
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, digest FROM api_tokens WHERE revoked_at IS NULL`)
	if err != nil {
		return fmt.Errorf("check token: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var id int64
	found := false
	for rows.Next() {
		var got []byte
		var rowID int64
		if err := rows.Scan(&rowID, &got); err != nil {
			return err
		}
		if subtle.ConstantTimeCompare(got, want) == 1 {
			id, found = rowID, true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !found {
		return ErrTokenUnknown
	}
	// A write on every request would be a lot for a value nobody reads often:
	// record the use at most once a minute.
	now := s.Now().UTC()
	_, _ = s.db.W.ExecContext(ctx,
		`UPDATE api_tokens SET last_used_at = ? WHERE id = ?
		   AND (last_used_at IS NULL OR last_used_at < ?)`,
		now.Format(time.RFC3339), id, now.Add(-time.Minute).Format(time.RFC3339))
	return nil
}

// DeleteAPIToken drops a revoked token's row. A live one has to be revoked
// first: the two-step keeps a stray click from removing a key something is
// still using.
func (s *Store) DeleteAPIToken(ctx context.Context, kind string, id int64) error {
	res, err := s.db.W.ExecContext(ctx,
		`DELETE FROM api_tokens WHERE id = ? AND kind = ? AND revoked_at IS NOT NULL`, id)
	if err != nil {
		return fmt.Errorf("delete token: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ReplaceAPITokenDigest swaps the digest in place, keeping the name and the
// created time: that is a rotation, not a new row, so the page's history
// stays one line per purpose and last_used_at restarts from the swap.
func (s *Store) ReplaceAPITokenDigest(ctx context.Context, kind string, id int64, token string) error {
	res, err := s.db.W.ExecContext(ctx,
		`UPDATE api_tokens SET digest = ?, created_at = ?, last_used_at = NULL, revoked_at = NULL WHERE id = ? AND kind = ? AND revoked_at IS NULL`,
		tokenDigest(token), s.Now().UTC().Format(time.RFC3339), id, kind)
	if err != nil {
		return fmt.Errorf("rotate token: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// RevokeAPIToken retires a token; the row stays so the page can say when.
func (s *Store) RevokeAPIToken(ctx context.Context, kind string, id int64) error {
	_, err := s.db.W.ExecContext(ctx,
		`UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND kind = ? AND revoked_at IS NULL`,
		s.Now().UTC().Format(time.RFC3339), id, kind)
	return err
}

// RevokeAPITokenByKind retires a token of one kind only, so the MCP page
// cannot revoke an API page token by guessing an id.
func (s *Store) RevokeAPITokenByKind(ctx context.Context, kind string, id int64) error {
	res, err := s.db.W.ExecContext(ctx,
		`UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND kind = ? AND revoked_at IS NULL`,
		s.Now().UTC().Format(time.RFC3339), id, kind)
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteAPITokens removes keys outright, revoked or not: a revoked key is still a
// row in a list someone has to read, and twenty of them from one afternoon of
// reconnecting is noise, not history.
func (s *Store) DeleteAPITokens(ctx context.Context, kind string, ids []int64) (int, error) {
	total := 0
	for _, id := range ids {
		res, err := s.db.W.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ? AND kind = ?`, id, kind)
		if err != nil {
			return total, fmt.Errorf("delete token %d: %w", id, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			total += int(n)
		}
	}
	return total, nil
}

// DeleteUnusedAPITokens removes keys of a kind that were never used and were
// made before cutoff. A key that never authenticated cannot be anyone's working
// client, and one made minutes ago may still be sitting in a paste buffer: the
// cutoff keeps the second case.
func (s *Store) DeleteUnusedAPITokens(ctx context.Context, kind string, cutoff time.Time) (int, error) {
	var n int64
	err := s.Write(ctx, func(tx *Tx) error {
		res, err := tx.tx.ExecContext(ctx, `DELETE FROM api_tokens
			WHERE kind = ? AND last_used_at IS NULL AND revoked_at IS NULL AND created_at < ?`,
			kind, ts(cutoff))
		if err != nil {
			return fmt.Errorf("delete unused tokens: %w", err)
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}
