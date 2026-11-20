// Package jev asks TypeSafe's System One model ("Jev") for a typed,
// calibrated judgement. It does not generate text: it answers a question with a
// choice and probabilities, in a few hundred milliseconds.
//
// The project's rule holds here too: a decision is a signal, not an
// authorization. Nothing in this package acts on its own - the caller decides
// what a "block" means, and the deterministic checks stay in front.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client speaks POST /v1/systemone.
type Client struct {
	BaseURL string // https://api.typesafe.ai
	Key     string
	Model   string // jev-latest
	HTTP    *http.Client
}

type question struct {
	Type         string         `json:"type"`
	Instructions string         `json:"instructions"`
	Criteria     map[string]any `json:"criteria,omitempty"`
}

type request struct {
	Model     string              `json:"model"`
	State     any                 `json:"state"`
	Questions map[string]question `json:"questions"`
}

type answer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
	Noul          float64            `json:"noul"`
}

type response struct {
	Model   string            `json:"model"`
	Answers map[string]answer `json:"answers"`
	Detail  json.RawMessage   `json:"detail"`
}

// Verdict is one answered choice question.
type Verdict struct {
	Model         string
	Choice        string
	Confidence    float64
	Probabilities map[string]float64
}

// maxBody bounds one answer: a decision is a few hundred bytes of JSON.
const maxBody = 64 << 10

// Choice asks which of the criteria best fits the state.
func (c *Client) Choice(ctx context.Context, state any, instruction string, criteria []string) (Verdict, error) {
	if len(criteria) < 2 {
		return Verdict{}, fmt.Errorf("jev: a choice needs at least two criteria")
	}
	crit := make(map[string]any, len(criteria))
	for _, k := range criteria {
		crit[k] = nil
	}
	out, err := c.ask(ctx, state, question{Type: "choice", Instructions: instruction, Criteria: crit})
	if err != nil {
		return Verdict{}, err
	}
	a, ok := out.Answers["q"]
	if !ok {
		return Verdict{}, fmt.Errorf("jev: no answer in the response")
	}
	return Verdict{Model: out.Model, Choice: a.Choice, Confidence: a.Confidence, Probabilities: a.Probabilities}, nil
}

// Probability asks for P(true) of one statement over the state.
func (c *Client) Probability(ctx context.Context, state any, instruction string) (float64, string, error) {
	out, err := c.ask(ctx, state, question{Type: "noul", Instructions: instruction})
	if err != nil {
		return 0, "", err
	}
	a, ok := out.Answers["q"]
	if !ok {
		return 0, "", fmt.Errorf("jev: no answer in the response")
	}
	return a.Noul, out.Model, nil
}

func (c *Client) ask(ctx context.Context, state any, q question) (response, error) {
	if c.BaseURL == "" || c.Key == "" {
		return response{}, fmt.Errorf("jev: no endpoint or key configured")
	}
	model := c.Model
	if model == "" {
		model = "jev-latest"
	}
	body, err := json.Marshal(request{Model: model, State: state, Questions: map[string]question{"q": q}})
	if err != nil {
		return response{}, err
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	url := strings.TrimRight(c.BaseURL, "/") + "/v1/systemone"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Key)
	res, err := httpClient.Do(req)
	if err != nil {
		return response{}, fmt.Errorf("jev: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return response{}, fmt.Errorf("jev: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	var out response
	if err := json.NewDecoder(io.LimitReader(res.Body, maxBody)).Decode(&out); err != nil {
		return response{}, fmt.Errorf("jev: %w", err)
	}
	return out, nil
}
