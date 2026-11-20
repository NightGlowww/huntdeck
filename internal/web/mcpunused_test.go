package web

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// The cleanup this pins: a list of 141 identical keys, nearly all of them
// minted by something that never came back. The rule is "never used", and a
// key that did authenticate must survive whatever its age.
func TestDeleteUnusedTokensSparesTheUsedOnes(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	unused, err := e.store.CreateAPIToken(ctx, "mcp", "unused", "secret-unused")
	if err != nil {
		t.Fatal(err)
	}
	used, err := e.store.CreateAPIToken(ctx, "mcp", "used", "secret-used")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.CheckAPIToken(ctx, "secret-used"); err != nil {
		t.Fatalf("marking the token used: %v", err)
	}

	// A cutoff past every key created here: the grace period is the handler's
	// business, and this is the store rule underneath it.
	n, err := e.store.DeleteUnusedAPITokens(ctx, "mcp", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("deleted %d keys, want the one that was never used", n)
	}
	all, _ := e.store.APITokens(ctx)
	left := map[int64]string{}
	for _, k := range all {
		left[k.ID] = k.Name
	}
	if _, ok := left[unused]; ok {
		t.Error("the never-used key survived")
	}
	if _, ok := left[used]; !ok {
		t.Error("the key that authenticated was deleted")
	}
}

// And the handler keeps keys made in the last hour: one may still be in a
// paste buffer, waiting for its first use.
func TestMCPDeleteUnusedKeepsKeysMadeJustNow(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	id, err := e.store.CreateAPIToken(context.Background(), "mcp", "just now", "secret-now")
	if err != nil {
		t.Fatal(err)
	}
	res, _ := e.do(c, "POST", "/mcp/delete-unused", url.Values{}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("delete unused: %d", res.StatusCode)
	}
	all, _ := e.store.APITokens(context.Background())
	found := false
	for _, k := range all {
		if k.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatal("a key created a moment ago was deleted")
	}
}
