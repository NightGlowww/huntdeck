package gobugfree

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLivePublicSmoke hits the real GObugfree public API when HUNTDECK_LIVE_TESTS=1.
// Never part of `make check`.
func TestLivePublicSmoke(t *testing.T) {
	if os.Getenv("HUNTDECK_LIVE_TESTS") != "1" {
		t.Skip("set HUNTDECK_LIVE_TESTS=1 to run live GObugfree smoke tests")
	}
	c, err := New(Options{PublicOnly: true, Rate: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	listing, err := c.ListPrograms(ctx)
	if err != nil {
		t.Fatalf("ListPrograms public: %v", err)
	}
	if len(listing.Programs) == 0 {
		t.Fatal("expected at least 1 public GObugfree program")
	}
}
