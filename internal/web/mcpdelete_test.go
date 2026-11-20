package web

import (
	"context"
	"fmt"
	"net/url"
	"testing"
)

// Deleting keys is the point of the list: revoking keeps a row forever, and one
// afternoon of reconnecting leaves twenty of them. The handler deletes one or
// many, and a key that is already gone is not an error.
func TestMCPDeleteRemovesTickedKeys(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	ctx := context.Background()

	ids := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		id, err := e.store.CreateAPIToken(ctx, "mcp", "MCP client", fmt.Sprintf("secret-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, fmt.Sprint(id))
	}

	res, _ := e.do(c, "POST", "/mcp/delete", url.Values{"id": ids[2:]}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("delete one: %d", res.StatusCode)
	}
	if got := mcpKeyCount(t, e); got != 2 {
		t.Fatalf("after one delete, %d keys left", got)
	}

	// The rest in one request, the way the bulk button sends them.
	res, _ = e.do(c, "POST", "/mcp/delete", url.Values{"id": ids[:2]}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("bulk delete: %d", res.StatusCode)
	}
	if got := mcpKeyCount(t, e); got != 0 {
		t.Fatalf("after the bulk delete, %d keys left", got)
	}

	// A key that is already gone: the page reloads, no error.
	res, _ = e.do(c, "POST", "/mcp/delete", url.Values{"id": {"999999"}}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("deleting a missing key: %d", res.StatusCode)
	}
}

func mcpKeyCount(t *testing.T, e *testEnv) int {
	t.Helper()
	all, err := e.store.APITokens(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, k := range all {
		if k.Kind == "mcp" {
			n++
		}
	}
	return n
}
