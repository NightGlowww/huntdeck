package pojia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Client speaks POJIA's stateless MCP endpoint: one POST per call, no session,
// the key as a bearer header. One client is meant to live for the process, so
// it carries the rate limiter; answers are cached by the caller.
type Client struct {
	BaseURL string
	Key     string
	HTTP    *http.Client

	mu   sync.Mutex
	last time.Time
}

// minInterval is the politeness floor between calls, whatever the cache does.
const minInterval = 500 * time.Millisecond

func (c *Client) throttle() {
	c.mu.Lock()
	wait := minInterval - time.Since(c.last)
	c.last = time.Now()
	c.mu.Unlock()
	if wait > 0 {
		time.Sleep(wait)
	}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcResponse struct {
	Result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// maxAnswer bounds a 200 response: POJIA answers are text, a megabyte is
// already far past anything it should send.
const maxAnswer = 1 << 20

// Call runs one tool and returns its first text answer. An HTTP-level failure
// is an error, not an answer: only POJIA's own "not in the index" wording
// counts as not-found, so a misconfigured endpoint cannot be cached as one.
func (c *Client) Call(ctx context.Context, tool string, args map[string]any) (string, error) {
	if c.BaseURL == "" || c.Key == "" {
		return "", fmt.Errorf("pojia: no endpoint or key configured")
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	c.throttle()
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call",
		Params: map[string]any{"name": tool, "arguments": args}})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.Key)
	res, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("pojia %s: %w", tool, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		_, _ = io.ReadAll(io.LimitReader(res.Body, 512))
		return "", fmt.Errorf("pojia %s: HTTP %d", tool, res.StatusCode)
	}
	var out rpcResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, maxAnswer)).Decode(&out); err != nil {
		return "", fmt.Errorf("pojia %s: %w", tool, err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("pojia %s: %s", tool, out.Error.Message)
	}
	if len(out.Result.Content) == 0 {
		return "", fmt.Errorf("pojia %s: empty answer", tool)
	}
	text := out.Result.Content[0].Text
	if out.Result.IsError {
		return "", fmt.Errorf("pojia %s: %s", tool, text)
	}
	return text, nil
}

// vendorUnknown is POJIA's own wording for a vendor it does not index. Matching
// it by phrase, not by HTTP status, keeps an unreachable endpoint from being
// recorded as "not indexed".
const vendorUnknown = "厂商不存在"

// ProgramProfile is the disclosed-report history of one program. found is
// false only when POJIA itself says it does not know the vendor.
func (c *Client) ProgramProfile(ctx context.Context, handle string) (profile string, found bool, err error) {
	profile, err = c.Call(ctx, "program_profile", map[string]any{"program": handle})
	if err != nil {
		if strings.Contains(err.Error(), vendorUnknown) {
			return "", false, nil
		}
		return "", false, err
	}
	return profile, true, nil
}
