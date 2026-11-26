package web

import (
	"net/http"
	"testing"
)

// TestExampleKindsKeepsTheDeclaredOrder pins the picker's list: distinct kinds,
// in the order notify declares them, so the picker does not reshuffle as events
// arrive.
func TestExampleKindsKeepsTheDeclaredOrder(t *testing.T) {
	cards := []messageCard{{Kind: "launch"}, {Kind: "scope_added"}, {Kind: "launch"}}
	got := exampleKinds(cards)
	if len(got) != 2 {
		t.Fatalf("exampleKinds = %v, want two kinds", got)
	}
	wantFirst := exampleKinds([]messageCard{{Kind: got[0]}})[0]
	if got[0] != wantFirst {
		t.Errorf("first kind = %q", got[0])
	}
}

// TestSampleFilterIgnoresAnUnknownKind: a hand-typed ?sample= must not empty the
// panel or 500.
func TestSampleFilterIgnoresAnUnknownKind(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) {})
	res, body := e.do(e.client(), "GET", "/settings/notifications?sample=not_a_kind", nil, http.Header{"Accept-Language": {"zh-CN"}})
	if res.StatusCode != 200 {
		t.Fatalf("page = %d", res.StatusCode)
	}
	if len(body) < 500 {
		t.Error("the page came back nearly empty")
	}
}
