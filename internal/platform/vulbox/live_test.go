package vulbox

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLivePublicSmoke hits the real VulBox public API when HUNTDECK_LIVE_TESTS=1.
// Never part of `make check`.
func TestLivePublicSmoke(t *testing.T) {
	if os.Getenv("HUNTDECK_LIVE_TESTS") != "1" {
		t.Skip("set HUNTDECK_LIVE_TESTS=1 to run live VulBox smoke tests")
	}
	c, err := New(Options{PublicOnly: true, Rate: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var page pagedProjects
	if err := c.getJSON(ctx, "/web/project/projects?type=-1&flag=0&order_by=time&direction=desc&page=1&page_size=10", nil, &page); err != nil {
		t.Fatalf("live public projects: %v", err)
	}
	if len(page.Data) == 0 {
		t.Fatal("expected at least 1 public VulBox project")
	}
}
