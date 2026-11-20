package web

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// A wall of identical rows is what this pins: the page lists the newest
// twenty and counts the rest, and ?all=1 still shows every one, because hiding
// data without a way back is worse than a long page.
func TestMCPPageShowsTheNewestKeysAndOffersTheRest(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	for i := 0; i < mcpKeysShown+5; i++ {
		if _, err := e.store.CreateAPIToken(context.Background(), "mcp", fmt.Sprintf("k%02d", i), fmt.Sprintf("secret-%02d", i)); err != nil {
			t.Fatal(err)
		}
	}

	_, body := e.do(c, "GET", "/mcp", nil, nil)
	if got := strings.Count(body, `aria-label="Select k`); got != mcpKeysShown {
		t.Fatalf("the page lists %d keys, want %d", got, mcpKeysShown)
	}
	if !strings.Contains(body, "5 older key(s) are not shown") && !strings.Contains(body, "%!d(string=5)") {
		t.Fatalf("the page does not say how many it hides")
	}

	_, all := e.do(c, "GET", "/mcp?all=1", nil, nil)
	if got := strings.Count(all, `aria-label="Select k`); got != mcpKeysShown+5 {
		t.Fatalf("?all=1 lists %d keys, want %d", got, mcpKeysShown+5)
	}
	_ = url.Values{}
}
