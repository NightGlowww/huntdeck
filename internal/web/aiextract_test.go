package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/ai"
)

// The button reads a policy with the configured model and stores what it finds.
// The whole path runs against a fake endpoint: no key, no network.
func TestExtractPoliciesFromSettings(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rules, err := json.Marshal([]map[string]string{{"title": "Send the header", "kind": "header"}})
		if err != nil {
			t.Error(err)
			return
		}
		body, err := json.Marshal(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": string(rules)}}},
		})
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer fake.Close()

	e := newTestEnv(t, func(d *Deps) {
		d.Password = "secret"
		d.AIBaseURL, d.AIModel = fake.URL, "fake"
		d.AI = &ai.Client{BaseURL: fake.URL, Model: "fake"}
	})
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if r, _ := e.do(c, http.MethodPost, "/login", urlValues("secret"), nil); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", r.StatusCode)
	}
	res, _ := e.do(c, http.MethodPost, "/settings/ai/extract", nil, nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("extract: %d", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); !strings.Contains(loc, "msg=ai-read") {
		t.Fatalf("location = %q, want a reading", loc)
	}
	n, err := e.store.CountAIRules(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("cached readings = %d, %v", n, err)
	}

	// The API shows the same reading, marked as a model's.
	_, page := e.do(c, http.MethodGet, "/api/v1/programs/fake/acme", nil, nil)
	if !strings.Contains(page, "ai_rules") || !strings.Contains(page, "Send the header") {
		t.Errorf("the API does not carry the reading: %.200s", page)
	}

	// A second press sends nothing: the policy hash is already cached.
	res2, _ := e.do(c, http.MethodPost, "/settings/ai/extract", nil, nil)
	if loc := res2.Header.Get("Location"); !strings.Contains(loc, "msg=ai-none") {
		t.Fatalf("second run = %q, want ai-none", loc)
	}
}
