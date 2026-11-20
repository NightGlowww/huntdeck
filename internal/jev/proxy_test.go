package jev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A proxy set on the client has to be used. It was silently ignored when the
// caller supplied HTTP as well: a provided client carries its own transport, so
// the proxy never reached it - measured as 451 direct where the proxy answers
// 200, in the doctor's own check.
//
// The proxy answers with a distinctive probability, so "the proxy was used" is
// decided by the value that came back rather than by a flag.
func TestProxyIsUsed(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model":"via-proxy","answers":{"q":{"type":"noul","noul":0.42}}}`))
	}))
	defer proxy.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model":"direct","answers":{"q":{"type":"noul","noul":0.90}}}`))
	}))
	defer api.Close()

	c := &Client{BaseURL: api.URL, Key: "k", Proxy: proxy.URL, Timeout: 5 * time.Second}
	p, model, err := c.Probability(context.Background(), map[string]any{"x": 1}, "is it?")
	if err != nil {
		t.Fatal(err)
	}
	if model != "via-proxy" || p != 0.42 {
		t.Fatalf("answered %q %.2f: the request went direct, not through the proxy", model, p)
	}
}

// Without a proxy the call goes direct: the other half of the same claim.
func TestNoProxyGoesDirect(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model":"direct","answers":{"q":{"type":"noul","noul":0.90}}}`))
	}))
	defer api.Close()
	c := &Client{BaseURL: api.URL, Key: "k", Timeout: 5 * time.Second}
	p, model, err := c.Probability(context.Background(), map[string]any{"x": 1}, "is it?")
	if err != nil {
		t.Fatal(err)
	}
	if model != "direct" || p != 0.90 {
		t.Fatalf("answered %q %.2f with no proxy configured", model, p)
	}
}
