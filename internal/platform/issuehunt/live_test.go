package issuehunt

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLivePublicSmoke hits the real IssueHunt public API when HUNTDECK_LIVE_TESTS=1.
// Never part of `make check`.
func TestLivePublicSmoke(t *testing.T) {
	if os.Getenv("HUNTDECK_LIVE_TESTS") != "1" {
		t.Skip("set HUNTDECK_LIVE_TESTS=1 to run live IssueHunt smoke tests")
	}
	c, err := New(Options{PublicOnly: true, Rate: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var page pagedPrograms
	if err := c.getJSON(ctx, "/programs?perPage=10&page=1", &page); err != nil {
		t.Fatalf("live public programs: %v", err)
	}
	if len(page.Data) == 0 {
		t.Fatal("expected at least 1 public IssueHunt program")
	}
}
