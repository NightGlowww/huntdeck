package triage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mdpsec/huntdeck/internal/jev"
)

// adviceServer answers the two probability questions with fixed values and the
// advisor with the confidence the test asks for, so the threshold is what is
// under test rather than the model.
func adviceServer(t *testing.T, accept, evidence, adviceConf float64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Questions map[string]struct {
				Type string `json:"type"`
			} `json:"questions"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		q := req.Questions["q"]
		var answer map[string]any
		switch q.Type {
		case "noul":
			// The first noul is acceptability, the second evidence: count them.
			n := counter()
			p := accept
			if n > 1 {
				p = evidence
			}
			answer = map[string]any{"type": "noul", "noul": p}
		case "choice":
			answer = map[string]any{"type": "choice", "choice": AdviceSecondIdentity,
				"confidence": adviceConf, "probabilities": map[string]float64{AdviceSecondIdentity: adviceConf}}
		}
		payload, _ := json.Marshal(map[string]any{
			"model": "jev-test", "answers": map[string]any{"q": answer},
		})
		_, _ = w.Write(payload)
	}))
}

var noulCalls int

func counter() int { noulCalls++; return noulCalls }

// The suggestion is shown only when the model was sure. Measured: 0.92 when the
// report had single-account traffic (the addition that moved the score from
// 0.22 to 0.72), 0.18-0.35 everywhere else.
func TestAdviceShowsOnlyWhenSure(t *testing.T) {
	for _, c := range []struct {
		name      string
		conf      float64
		wantShown bool
	}{
		{"sure, 0.92", 0.92, true},
		{"marginal, 0.55", 0.55, false},
		{"guessing, 0.18", 0.18, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			noulCalls = 0
			srv := adviceServer(t, 0.80, 0.40, c.conf)
			defer srv.Close()
			client := &jev.Client{BaseURL: srv.URL, Key: "k"}
			v, err := Judge(context.Background(), client, Finding{
				Title: "t", InScope: "in scope", Impact: "read another tenant's data", Evidence: "raw traffic",
			})
			if err != nil {
				t.Fatal(err)
			}
			shown := v.Advice != ""
			if shown != c.wantShown {
				t.Fatalf("advice %q at confidence %.2f, want shown=%v", v.Advice, c.conf, c.wantShown)
			}
		})
	}
}
