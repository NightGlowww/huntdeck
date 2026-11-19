package pojia

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

// Client speaks POJIA's stateless MCP endpoint: one POST per call, no session,
// the key as a bearer header. POJIA indexes publicly disclosed bug bounty
// reports with Chinese annotations, so its answers are text.
type Client struct {
	BaseURL string
	Key     string
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
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Call runs one tool and returns its first text answer.
func (c *Client) Call(ctx context.Context, tool string, args map[string]any) (string, error) {
	if c.BaseURL == "" || c.Key == "" {
		return "", fmt.Errorf("pojia: no endpoint or key configured")
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
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
		b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return "", fmt.Errorf("pojia %s: HTTP %d: %s", tool, res.StatusCode, string(b))
	}
	var out rpcResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
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
		return text, fmt.Errorf("pojia: %s", text)
	}
	return text, nil
}

// ProgramProfile is the disclosed-report history of one program: counts, the
// bounty total and the vulnerability-class distribution. The error is nil and
// found false when POJIA does not know the vendor.
func (c *Client) ProgramProfile(ctx context.Context, handle string) (profile string, found bool, err error) {
	profile, err = c.Call(ctx, "program_profile", map[string]any{"program": handle})
	if err != nil {
		// "厂商不存在" is an answer, not a failure.
		if strings.Contains(err.Error(), "厂商不存在") || strings.Contains(err.Error(), "404") {
			return "", false, nil
		}
		return "", false, err
	}
	return profile, true, nil
}
