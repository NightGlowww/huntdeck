// Package jev asks TypeSafe's System One model ("Jev") for typed, calibrated
// judgements. It does not generate text: it answers questions with structured
// values in a few hundred milliseconds.
//
// The shapes follow the vendor's manual (docs.typesafe.ai), which says three
// things this package acts on:
//
//   - any mix of question types can go in one request, evaluated in parallel and
//     in isolation against the same state, and "adding questions barely changes
//     the response time" - so the six-rule policy check is one call, not six;
//   - a Choice's criteria are a map of option -> description, and the
//     descriptions are sent to the model, so they are how options are separated;
//   - three primitives exist: Choice (pick one), Score (a position on ordered
//     levels) and Noul (true/false). Score was the one missing here.
//
// The project's rule holds throughout: a decision is a signal, not an
// authorization. Nothing in this package acts on its own.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client speaks POST /v1/systemone.
type Client struct {
	BaseURL string // https://api.typesafe.ai
	Key     string
	Model   string // jev-latest
	HTTP    *http.Client
	// Proxy is an HTTP proxy the call leaves through, when a direct egress is
	// blocked: this deployment reaches TypeSafe only through the local proxy.
	Proxy string
	// Timeout bounds one call. Leave it zero for the 20s default; a caller that
	// wants a shorter one sets this rather than supplying HTTP, because a client
	// passed in HTTP has its own transport and would bypass Proxy silently.
	Timeout time.Duration
}

// Question is one typed question. Criteria is a map[string]string for a choice
// (option -> the description that separates it from the others) and an ordered
// []string of levels, low to high, for a score.
type Question struct {
	Type         string
	Instructions string
	Criteria     any
}

// Choice builds a choice question from option -> description. A description may
// be empty, but an empty map is how options stop being distinguishable.
func Choice(instructions string, options map[string]string) Question {
	return Question{Type: "choice", Instructions: instructions, Criteria: options}
}

// Score builds a score question from ordered levels, low to high (2 to 10).
func Score(instructions string, levels []string) Question {
	return Question{Type: "score", Instructions: instructions, Criteria: levels}
}

// Noul builds a true/false question.
func Noul(instructions string) Question {
	return Question{Type: "noul", Instructions: instructions}
}

// Answer is one question's answer, whatever its type.
type Answer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Score         float64            `json:"score"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
	Noul          float64            `json:"noul"`
}

// Verdict is one answered choice question.
type Verdict struct {
	Model         string
	Choice        string
	Confidence    float64
	Probabilities map[string]float64
}

// Rating is one answered score question: the position, and the probability of
// each level it was scored against.
type Rating struct {
	Model      string
	Score      float64
	Confidence float64
	Levels     map[string]float64
}

// maxBody bounds one answer: a decision is a few hundred bytes of JSON.
const maxBody = 256 << 10

type wireQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

type wireRequest struct {
	Model     string                  `json:"model"`
	State     any                     `json:"state"`
	Questions map[string]wireQuestion `json:"questions"`
}

type wireResponse struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Detail  json.RawMessage   `json:"detail"`
}

// Ask sends every question in one request and returns the answers by id. This
// is the shape the vendor recommends: one state, many questions, evaluated in
// parallel - a second question costs a little input, not a second round trip.
func (c *Client) Ask(ctx context.Context, state any, questions map[string]Question) (map[string]Answer, string, error) {
	if c.BaseURL == "" || c.Key == "" {
		return nil, "", fmt.Errorf("jev: no endpoint or key configured")
	}
	if len(questions) == 0 {
		return nil, "", fmt.Errorf("jev: no questions")
	}
	model := c.Model
	if model == "" {
		model = "jev-latest"
	}
	wire := make(map[string]wireQuestion, len(questions))
	for id, q := range questions {
		wire[id] = wireQuestion(q)
	}
	body, err := json.Marshal(wireRequest{Model: model, State: state, Questions: wire})
	if err != nil {
		return nil, "", err
	}
	httpClient := c.HTTP
	if httpClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if c.Proxy != "" {
			u, perr := url.Parse(c.Proxy)
			if perr != nil {
				return nil, "", fmt.Errorf("jev: bad proxy %q: %w", c.Proxy, perr)
			}
			transport.Proxy = http.ProxyURL(u)
		}
		timeout := c.Timeout
		if timeout == 0 {
			timeout = 20 * time.Second
		}
		httpClient = &http.Client{Timeout: timeout, Transport: transport}
	}
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/v1/systemone"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Key)
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("jev: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return nil, "", fmt.Errorf("jev: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	var out wireResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, maxBody)).Decode(&out); err != nil {
		return nil, "", fmt.Errorf("jev: %w", err)
	}
	if len(out.Answers) == 0 && len(out.Detail) > 0 {
		return nil, "", fmt.Errorf("jev: %s", strings.TrimSpace(string(out.Detail)))
	}
	return out.Answers, out.Model, nil
}

// Choice asks which of the options best fits the state. The options map to their
// descriptions; an option with an empty description still works, it is just less
// separated from its neighbours.
func (c *Client) Choice(ctx context.Context, state any, instruction string, options map[string]string) (Verdict, error) {
	if len(options) < 2 {
		return Verdict{}, fmt.Errorf("jev: a choice needs at least two options")
	}
	answers, model, err := c.Ask(ctx, state, map[string]Question{"q": Choice(instruction, options)})
	if err != nil {
		return Verdict{}, err
	}
	a, ok := answers["q"]
	if !ok {
		return Verdict{}, fmt.Errorf("jev: no answer in the response")
	}
	return Verdict{Model: model, Choice: a.Choice, Confidence: a.Confidence, Probabilities: a.Probabilities}, nil
}

// Probability asks for P(true) of one statement over the state.
func (c *Client) Probability(ctx context.Context, state any, instruction string) (float64, string, error) {
	answers, model, err := c.Ask(ctx, state, map[string]Question{"q": Noul(instruction)})
	if err != nil {
		return 0, "", err
	}
	a, ok := answers["q"]
	if !ok {
		return 0, "", fmt.Errorf("jev: no answer in the response")
	}
	return a.Noul, model, nil
}

// Rating asks for a position on ordered levels. Levels are the rubric, low to
// high; a Score answer may sit between two of them, which is what makes it a
// rating rather than a choice.
func (c *Client) Rating(ctx context.Context, state any, instruction string, levels []string) (Rating, error) {
	if len(levels) < 2 {
		return Rating{}, fmt.Errorf("jev: a score needs at least two levels")
	}
	answers, model, err := c.Ask(ctx, state, map[string]Question{"q": Score(instruction, levels)})
	if err != nil {
		return Rating{}, err
	}
	a, ok := answers["q"]
	if !ok {
		return Rating{}, fmt.Errorf("jev: no answer in the response")
	}
	return Rating{Model: model, Score: a.Score, Confidence: a.Confidence, Levels: a.Probabilities}, nil
}
