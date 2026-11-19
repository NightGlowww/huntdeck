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
	ID         int64
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
func (s *Store) CreateAPIToken(ctx context.Context, name, token string) (int64, error) {
	res, err := s.db.W.ExecContext(ctx,
		`INSERT INTO api_tokens (name, digest, created_at) VALUES (?, ?, ?)`,
		name, tokenDigest(token), s.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("create token: %w", err)
	}
	return res.LastInsertId()
}

// APITokens lists every token, newest first, revoked ones included so the page
// can say what was retired.
func (s *Store) APITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := s.db.R.QueryContext(ctx,
		`SELECT id, name, created_at, last_used_at, revoked_at FROM api_tokens ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		var created string
		var last, revoked sql.NullString
		if err := rows.Scan(&t.ID, &t.Name, &created, &last, &revoked); err != nil {
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
func (s *Store) DeleteAPIToken(ctx context.Context, id int64) error {
	res, err := s.db.W.ExecContext(ctx,
		`DELETE FROM api_tokens WHERE id = ? AND revoked_at IS NOT NULL`, id)
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
func (s *Store) ReplaceAPITokenDigest(ctx context.Context, id int64, token string) error {
	res, err := s.db.W.ExecContext(ctx,
		`UPDATE api_tokens SET digest = ?, created_at = ?, last_used_at = NULL, revoked_at = NULL WHERE id = ?`,
		tokenDigest(token), s.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("rotate token: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// RevokeAPIToken retires a token; the row stays so the page can say when.
func (s *Store) RevokeAPIToken(ctx context.Context, id int64) error {
	_, err := s.db.W.ExecContext(ctx,
		`UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		s.Now().UTC().Format(time.RFC3339), id)
	return err
}
