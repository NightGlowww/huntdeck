package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var ErrPojiaCacheMiss = errors.New("no cached POJIA answer")

// PojiaCacheKey is the cache key for one POJIA call: the tool and its
// arguments, hashed, so any argument change is a different key.
func PojiaCacheKey(tool string, args map[string]any) string {
	names := make([]string, 0, len(args))
	for k := range args {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, k := range names {
		parts = append(parts, fmt.Sprintf("%s=%v", k, args[k]))
	}
	sum := sha256.Sum256([]byte(tool + "\x00" + strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])
}

// PojiaCached reads one cached answer, if it is younger than ttl.
func (s *Store) PojiaCached(ctx context.Context, key string, ttl time.Duration) (string, error) {
	var answer, at string
	err := s.db.R.QueryRowContext(ctx,
		`SELECT answer, fetched_at FROM pojia_cache WHERE key = ?`, key).Scan(&answer, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrPojiaCacheMiss
	}
	if err != nil {
		return "", err
	}
	fetched, _ := time.Parse(time.RFC3339, at)
	if time.Since(fetched) >= ttl {
		return "", ErrPojiaCacheMiss
	}
	return answer, nil
}

// pojiaCacheKeep is how long an unused answer may sit before the next save
// sweeps it; the read TTL already hides older rows.
const pojiaCacheKeep = 30 * 24 * time.Hour

// SavePojiaCache stores one answer and, in the same write, drops rows nobody
// has refreshed in a month, so the table cannot grow without end.
func (s *Store) SavePojiaCache(ctx context.Context, key, answer string) error {
	return s.Write(ctx, func(tx *Tx) error {
		if _, err := tx.tx.ExecContext(ctx, `INSERT INTO pojia_cache (key, answer, fetched_at)
			VALUES (?, ?, ?) ON CONFLICT (key) DO UPDATE SET answer = excluded.answer,
			 fetched_at = excluded.fetched_at`,
			key, answer, s.Now().UTC().Format(time.RFC3339)); err != nil {
			return err
		}
		_, err := tx.tx.ExecContext(ctx, `DELETE FROM pojia_cache WHERE fetched_at < ?`,
			s.Now().Add(-pojiaCacheKeep).UTC().Format(time.RFC3339))
		return err
	})
}
