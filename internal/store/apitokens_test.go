package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
)

// A token is a script's key to the same single gate. Only its digest is stored,
// so a copy of the database cannot be replayed, and revoking is a row update.
func TestAPITokens(t *testing.T) {
	s := newTestStore(t, nil)
	ctx := context.Background()

	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	token := hex.EncodeToString(buf)

	id, err := s.CreateAPIToken(ctx, "api", "ci", token)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckAPIToken(ctx, token); err != nil {
		t.Fatalf("a fresh token must work: %v", err)
	}
	if err := s.CheckAPIToken(ctx, token+"x"); !errors.Is(err, ErrTokenUnknown) {
		t.Fatalf("a wrong token must be unknown, got %v", err)
	}

	// The digest is what is stored: the token itself never reaches the row.
	var stored []byte
	if err := s.db.R.QueryRowContext(ctx, `SELECT digest FROM api_tokens WHERE id = ?`, id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) == token {
		t.Fatal("the token was stored in clear")
	}

	if err := s.RevokeAPIToken(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckAPIToken(ctx, token); !errors.Is(err, ErrTokenUnknown) {
		t.Fatalf("a revoked token must stop working, got %v", err)
	}

	list, err := s.APITokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].RevokedAt == nil || list[0].Name != "ci" {
		t.Fatalf("the list must keep the retired token with its name: %+v", list)
	}
}
