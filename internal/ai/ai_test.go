package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A well-behaved endpoint: one exchange, JSON back.
func TestCompleteAndJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("key not sent: %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["temperature"] != float64(0) {
			t.Errorf("temperature %v, want 0", body["temperature"])
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, APIKey: "k", Model: "m"}
	var out struct {
		OK bool `json:"ok"`
	}
	if err := c.CompleteJSON(context.Background(), "system", "user", &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Error("the JSON answer was not decoded")
	}
}

// A refusal and a rate limit are told apart, so a caller can back off or
// surface the key problem.
func TestErrorsAreClassified(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{401, "refused the key"},
		{429, "rate limited"},
		{500, "HTTP 500"},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(`{"error":{"message":"nope"}}`))
		}))
		client := &Client{BaseURL: srv.URL, Model: "m"}
		_, err := client.Complete(context.Background(), "s", "u", 0)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("status %d: %v", c.status, err)
		}
		srv.Close()
	}
}

// No endpoint configured is a state, not a failure: the caller renders its
// offline path.
func TestUnconfigured(t *testing.T) {
	c := &Client{}
	if c.Configured() {
		t.Fatal("an empty client claims to be configured")
	}
	if _, err := c.Complete(context.Background(), "s", "u", 0); !errors.Is(err, ErrNoKey) {
		t.Fatalf("unconfigured: %v", err)
	}
}

// A fenced answer is still read.
func TestExtractJSONFromAFence(t *testing.T) {
	got := extractJSON("Here you go:\n\n```json\n{\"a\":1}\n```\n")
	if got != `{"a":1}` {
		t.Fatalf("extracted %q", got)
	}
}

// "null" is not an answer: a struct left untouched would look decoded.
func TestNullIsNotAnAnswer(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"null"}}]}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Model: "m"}
	var out struct {
		A string `json:"a"`
	}
	err := c.CompleteJSON(context.Background(), "s", "u", &out)
	if err == nil {
		t.Fatalf("null was accepted (calls=%d)", calls)
	}
}

// A brace inside a JSON string must not end the object.
func TestExtractJSONIgnoresBracesInStrings(t *testing.T) {
	got := extractJSON(`prefix {"note":"a } inside","ok":true} suffix`)
	want := `{"note":"a } inside","ok":true}`
	if got != want {
		t.Fatalf("got %q", got)
	}
}
