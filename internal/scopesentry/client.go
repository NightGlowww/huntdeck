package scopesentry

import (
	"bufio"
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

// Client speaks ScopeSentry's MCP endpoint: one POST per JSON-RPC call with an
// ssk_ key, answered either as plain JSON or as an SSE frame ("event: message"
// then "data: {...}"), which is what the server sends today.
type Client struct {
	BaseURL string // e.g. http://10.0.8.4:8082
	Key     string // ssk_…
	HTTP    *http.Client
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
		Tools   []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// maxBody bounds one answer: ScopeSentry's tools answer JSON, a megabyte is
// already past anything they should send.
const maxBody = 1 << 20

// Call runs one tool and returns its first text answer.
// ErrRateLimited is the scanner refusing for a moment: the caller should stop
// and try again later, not treat the pass as failed.
var ErrRateLimited = errors.New("scopesentry: rate limited")

func (c *Client) Call(ctx context.Context, tool string, args map[string]any) (string, error) {
	if c.BaseURL == "" || c.Key == "" {
		return "", fmt.Errorf("scopesentry: no endpoint or key configured")
	}
	return c.do(ctx, rpcRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call",
		Params: map[string]any{"name": tool, "arguments": args}})
}

// Tools lists the tool names this key may use — the honest way to say whether
// a key is scoped tightly enough before anything is created.
func (c *Client) Tools(ctx context.Context) ([]string, error) {
	raw, err := c.do(ctx, rpcRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list"})
	if err != nil {
		return nil, err
	}
	// The answer is the whole JSON-RPC envelope: result.tools, not tools.
	var out struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("scopesentry: tools/list: %w", err)
	}
	names := make([]string, 0, len(out.Result.Tools))
	for _, t := range out.Result.Tools {
		names = append(names, t.Name)
	}
	return names, nil
}

func (c *Client) do(ctx context.Context, req rpcRequest) (string, error) {
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	url := strings.TrimRight(c.BaseURL, "/") + "/mcp"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	httpReq.Header.Set("X-API-Key", c.Key)
	res, err := httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("scopesentry %s: %w", req.Method, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusTooManyRequests {
		_, _ = io.ReadAll(io.LimitReader(res.Body, 512))
		return "", ErrRateLimited
	}
	if res.StatusCode != http.StatusOK {
		_, _ = io.ReadAll(io.LimitReader(res.Body, 512))
		return "", fmt.Errorf("scopesentry %s: HTTP %d", req.Method, res.StatusCode)
	}
	payload, err := readJSONOrSSE(res.Body)
	if err != nil {
		return "", fmt.Errorf("scopesentry %s: %w", req.Method, err)
	}
	var out rpcResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		return "", fmt.Errorf("scopesentry %s: %w", req.Method, err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("scopesentry %s: %s", req.Method, out.Error.Message)
	}
	if req.Method == "tools/list" {
		return string(payload), nil
	}
	if len(out.Result.Content) == 0 {
		return "", fmt.Errorf("scopesentry %s: empty answer", req.Method)
	}
	text := out.Result.Content[0].Text
	if out.Result.IsError {
		return "", fmt.Errorf("scopesentry: %s", text)
	}
	return text, nil
}

// readJSONOrSSE takes the body whichever way the server framed it: a bare JSON
// document, or SSE frames whose last "data:" line is the message.
func readJSONOrSSE(r io.Reader) ([]byte, error) {
	limited := io.LimitReader(r, maxBody)
	buf, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(buf)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return trimmed, nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(buf))
	scanner.Buffer(make([]byte, 0, 64<<10), maxBody)
	var last []byte
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		last = []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(last) == 0 {
		return nil, fmt.Errorf("no JSON in the answer")
	}
	return last, nil
}
