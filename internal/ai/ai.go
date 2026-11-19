// Package ai talks to an OpenAI-compatible endpoint: one POST per request,
// /chat/completions, JSON back. It exists so a feature that wants a model can
// ask for one without the rest of HuntDeck learning a vendor SDK — and so that
// "no key configured" stays a normal state, not an error path.
//
// Everything here is optional: an install without AI_BASE_URL never constructs
// a Client, and every feature that uses one must render without it
// (docs/design/ai-layer.md).
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is one OpenAI-compatible endpoint.
type Client struct {
	BaseURL string // e.g. https://api.deepseek.com/v1
	APIKey  string
	Model   string
	HTTP    *http.Client // nil for a client with a 60 s timeout
}

// ErrNoKey says the install has no AI key: callers render their offline state.
var ErrNoKey = errors.New("ai: no key configured")

// Configured reports whether this client can make a call.
func (c *Client) Configured() bool { return c != nil && c.BaseURL != "" && c.Model != "" }

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type request struct {
	Model       string    `json:"model"`
	Messages    []message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature"`
	// ResponseFormat asks for JSON where the endpoint supports it; a plain
	// chat endpoint ignores it and the parser copes.
	ResponseFormat map[string]string `json:"response_format,omitempty"`
}

type response struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Complete sends one exchange and returns the model's text.
func (c *Client) Complete(ctx context.Context, system, user string, maxTokens int) (string, error) {
	if !c.Configured() {
		return "", ErrNoKey
	}
	body := request{
		Model:     c.Model,
		Messages:  []message{{"system", system}, {"user", user}},
		MaxTokens: maxTokens,
		// Deterministic where the endpoint allows it: a ranking or a summary
		// that changes between identical calls is worse than useless here.
		Temperature: 0,
	}
	raw, err := c.post(ctx, body)
	if err != nil {
		return "", err
	}
	if len(raw.Choices) == 0 {
		return "", fmt.Errorf("ai: the answer held no choices")
	}
	return strings.TrimSpace(raw.Choices[0].Message.Content), nil
}

// CompleteJSON asks for JSON and decodes it into out, retrying once with the
// parse error appended when the model wraps its answer in prose or a fence.
func (c *Client) CompleteJSON(ctx context.Context, system, user string, out any) error {
	if !c.Configured() {
		return ErrNoKey
	}
	answer, err := c.Complete(ctx, system, user, 0)
	if err != nil {
		return err
	}
	// "null" unmarshals into a struct leaving it untouched, which is not an
	// answer: treat it like prose and ask again.
	if body := extractJSON(answer); body != "null" {
		if err := json.Unmarshal([]byte(body), out); err == nil {
			return nil
		}
	}
	retry := user + "\n\nYour previous answer was not valid JSON. Answer with JSON only, no prose and no code fence."
	answer, err = c.Complete(ctx, system, retry, 0)
	if err != nil {
		return err
	}
	body := extractJSON(answer)
	if body == "null" {
		return fmt.Errorf("ai: the answer was null")
	}
	if err := json.Unmarshal([]byte(body), out); err != nil {
		return fmt.Errorf("ai: the answer was not JSON: %w", err)
	}
	return nil
}

func (c *Client) post(ctx context.Context, body request) (*response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ai: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("ai: the endpoint refused the key (%d)", res.StatusCode)
	case res.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("ai: rate limited")
	case res.StatusCode >= 400:
		return nil, fmt.Errorf("ai: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out response
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("ai: cannot read the answer: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("ai: %s", out.Error.Message)
	}
	return &out, nil
}

// extractJSON takes the first JSON object or array out of an answer that may
// carry a code fence or a sentence around it.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if j := strings.IndexAny(rest, "\n"); j >= 0 && !strings.HasPrefix(rest, "{") && !strings.HasPrefix(rest, "[") {
			rest = rest[j+1:]
		}
		if k := strings.Index(rest, "```"); k >= 0 {
			s = rest[:k]
		}
	}
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return s
	}
	// Braces inside JSON strings do not open or close the object: a password
	// or a policy quote can hold either.
	depth := 0
	inString, escaped := false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s[start:]
}
