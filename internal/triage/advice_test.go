package triage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mdpsec/huntdeck/internal/jev"
)

// adviceServer answers by question id, in one request: the judge now sends the
// two nouls and the severity score together, so a fake that answers "q" answers
// nothing the caller reads.
func adviceServer(t *testing.T, accept, evidence, adviceConf float64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Questions map[string]struct {
				Type string `json:"type"`
			} `json:"questions"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		answers := map[string]any{}
		for id, q := range req.Questions {
			switch q.Type {
			case "noul":
				p := evidence
				if id == qAccept {
					p = accept
				}
				answers[id] = map[string]any{"type": "noul", "noul": p}
			case "score":
				answers[id] = map[string]any{"type": "score", "score": 0.6, "confidence": 0.5,
					"probabilities": map[string]float64{"medium: real impact": 0.5}}
			case "choice":
				answers[id] = map[string]any{"type": "choice", "choice": AdviceSecondIdentity,
					"confidence": adviceConf, "probabilities": map[string]float64{AdviceSecondIdentity: adviceConf}}
			}
		}
		payload, _ := json.Marshal(map[string]any{"model": "jev-test", "answers": answers})
		_, _ = w.Write(payload)
	}))
}

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
		{"the right suggestion at 0.57", 0.57, true},
		{"above the guess band, 0.52", 0.52, true},
		{"a guess, 0.35", 0.35, false},
		{"guessing, 0.18", 0.18, false},
	} {
		t.Run(c.name, func(t *testing.T) {
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

// The severity comes back with the other two answers, from the same request.
func TestJudgeCarriesTheSeverity(t *testing.T) {
	srv := adviceServer(t, 0.85, 0.80, 0.1)
	defer srv.Close()
	v, err := Judge(context.Background(), &jev.Client{BaseURL: srv.URL, Key: "k"}, Finding{
		Title: "t", InScope: "in scope", Impact: "read another tenant's data", Evidence: "two accounts",
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.Call != CallSubmittable {
		t.Fatalf("call %s, want submittable", v.Call)
	}
	if v.Severity != 0.6 || len(v.SeverityLevels) == 0 {
		t.Fatalf("severity %.2f with %d levels", v.Severity, len(v.SeverityLevels))
	}
}
