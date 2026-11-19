package policyai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mdpsec/huntdeck/internal/ai"
)

// fakeModel answers with one rule, built with json.Marshal so no hand-written
// escaping can be wrong.
func fakeModel(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		content, err := json.Marshal([]map[string]string{{"title": "Send the header", "kind": "header"}})
		if err != nil {
			t.Error(err)
			return
		}
		body, err := json.Marshal(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": string(content)}}},
		})
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
}

func TestExtractAgainstFake(t *testing.T) {
	fake := fakeModel(t)
	defer fake.Close()
	c := &ai.Client{BaseURL: fake.URL, Model: "fake"}
	var out []struct {
		Title string `json:"title"`
	}
	if err := c.CompleteJSON(context.Background(), "sys", "user", &out); err != nil {
		t.Fatalf("CompleteJSON: %v", err)
	}
	if len(out) != 1 || out[0].Title != "Send the header" {
		t.Fatalf("decoded %+v", out)
	}
}
