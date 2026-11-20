package jev

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Each provider is wired against the request and response its own documentation
// shows. Without keys these cannot be called for real, so what is verified here
// is the shape: the path, the auth header, the body fields and the answer
// parsing. A provider that answers differently from its manual fails this.

func questions() map[string]Question {
	return map[string]Question{
		"urgent":   Noul("Is this urgent?"),
		"team":     Choice("Which team?", map[string]string{"billing": "Payments", "technical": "Outages"}),
		"severity": Score("How severe?", []string{"No impact", "Minor: small", "Critical"}),
	}
}

func TestOpenAIRequestBodyAndAnswer(t *testing.T) {
	var got map[string]any
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		_, _ = w.Write([]byte(`{"answers":[
			{"type":"predicate","name":"urgent","probability":0.82},
			{"type":"choice","name":"team","choice":"billing","confidence":0.9,
			  "probabilities":[{"value":"billing","probability":0.9},{"value":"technical","probability":0.1}]},
			{"type":"score","name":"severity","score":2.4,"confidence":0.7,"probabilities":[{"value":"Critical","probability":0.6}]}]}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Key: "k", Provider: ProviderOpenAI, Model: "gpt-6-luna"}
	a, model, err := c.Ask(context.Background(), map[string]any{"state": "x"}, questions())
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/decisions" || auth != "Bearer k" {
		t.Fatalf("path %q auth %q", path, auth)
	}
	qs, _ := got["questions"].([]any)
	if len(qs) != 3 {
		t.Fatalf("%d questions sent", len(qs))
	}
	types := map[string]bool{}
	for _, q := range qs {
		m := q.(map[string]any)
		types[m["type"].(string)] = true
		if m["name"] == nil || m["name"] == "" {
			t.Error("a question without a name: their answers are keyed by it")
		}
	}
	for _, want := range []string{"predicate", "choice", "score"} {
		if !types[want] {
			t.Errorf("no %q question in %v", want, types)
		}
	}
	if a["urgent"].Noul != 0.82 {
		t.Errorf("predicate read as %v", a["urgent"].Noul)
	}
	if a["team"].Choice != "billing" || a["team"].Probabilities["technical"] != 0.1 {
		t.Errorf("choice read as %+v", a["team"])
	}
	if a["severity"].Score != 2.4 {
		t.Errorf("score read as %v", a["severity"].Score)
	}
	if model != "gpt-6-luna" {
		t.Errorf("model %q", model)
	}
}

// A refusal is the model declining, which is not a zero probability.
func TestOpenAIRefusalIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answers":[{"type":"refusal","name":"urgent"}]}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Key: "k", Provider: ProviderOpenAI}
	_, _, err := c.Ask(context.Background(), "x", map[string]Question{"urgent": Noul("?")})
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("a refusal came back as %v", err)
	}
}

// Cloudflare runs the model through Workers AI: the path carries the account,
// and the answer sits inside a result envelope.
func TestCloudflarePathAndEnvelope(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"result":{"model":"clef-flash","answers":{"urgent":{"type":"noul","noul":0.66}}},"success":true,"errors":[]}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Key: "k", Provider: ProviderCloudflare, AccountID: "acct1"}
	a, _, err := c.Ask(context.Background(), "x", map[string]Question{"urgent": Noul("?")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(path, "/accounts/acct1/ai/run/@cf/cloudflare/clef-flash") {
		t.Fatalf("path %q", path)
	}
	if a["urgent"].Noul != 0.66 {
		t.Fatalf("envelope not unwrapped: %+v", a["urgent"])
	}
}

// Cloudflare without an account id cannot build a path, and saying so beats
// sending the request somewhere wrong.
func TestCloudflareNeedsAnAccount(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1", Key: "k", Provider: ProviderCloudflare}
	_, _, err := c.Ask(context.Background(), "x", map[string]Question{"urgent": Noul("?")})
	if err == nil || !strings.Contains(err.Error(), "account id") {
		t.Fatalf("got %v", err)
	}
}

// Bailian speaks the same protocol as TypeSafe: a bare base URL, a workspace
// header when there is one, and answers keyed by question id.
func TestBailianUsesTheBaseURLAndWorkspaceHeader(t *testing.T) {
	var path, ws string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, ws = r.URL.Path, r.Header.Get("X-Bailian-Workspace-Id")
		_, _ = w.Write([]byte(`{"model":"decision-model-preview","answers":{"urgent":{"type":"noul","noul":0.4}}}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL + "/decision", Key: "k", Provider: ProviderBailian, WorkspaceID: "ws_1"}
	a, _, err := c.Ask(context.Background(), "x", map[string]Question{"urgent": Noul("?")})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/decision" || ws != "ws_1" {
		t.Fatalf("path %q workspace %q", path, ws)
	}
	if a["urgent"].Noul != 0.4 {
		t.Fatalf("answer %+v", a["urgent"])
	}
}

// The smallest documented ceiling wins: a caller must not be able to send a
// request that one provider would accept and another reject.
func TestQuestionCeiling(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1", Key: "k"}
	many := map[string]Question{}
	for i := 0; i < MaxQuestions+1; i++ {
		many[string(rune('a'+i))] = Noul("?")
	}
	_, _, err := c.Ask(context.Background(), "x", many)
	if err == nil || !strings.Contains(err.Error(), "smallest provider ceiling") {
		t.Fatalf("got %v", err)
	}
}
