package inspectiv

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveTokenSmoke hits the real Inspectiv API when HUNTDECK_LIVE_TESTS=1
// and INS_TOKEN is set. Never part of `make check`.
func TestLiveTokenSmoke(t *testing.T) {
	if os.Getenv("HUNTDECK_LIVE_TESTS") != "1" {
		t.Skip("set HUNTDECK_LIVE_TESTS=1 to run live Inspectiv smoke tests")
	}
	tok := os.Getenv("INS_TOKEN")
	if tok == "" {
		t.Skip("set INS_TOKEN to run live Inspectiv smoke tests")
	}

	c, err := New(Options{Token: tok, Rate: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := c.KeepAlive(ctx); err != nil {
		t.Fatalf("KeepAlive: %v", err)
	}
	if c.Account() == "" {
		t.Fatal("expected non-empty Account() from /accounts/profile/")
	}
	listing, err := c.ListPrograms(ctx)
	if err != nil {
		t.Fatalf("ListPrograms: %v", err)
	}
	if !listing.Complete || len(listing.Programs) == 0 {
		t.Fatalf("expected complete non-empty program listing, got complete=%v len=%d", listing.Complete, len(listing.Programs))
	}
}
