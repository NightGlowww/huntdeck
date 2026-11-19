package scopesentry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// MemAvailablePercent reads the scanner host's free memory from its /healthz.
// It is the number the handoff checks before handing over more work.
func (c *Client) MemAvailablePercent(ctx context.Context) (float64, bool, error) {
	if c.BaseURL == "" {
		return 0, false, fmt.Errorf("scopesentry: no endpoint configured")
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 8 * time.Second}
	}
	url := strings.TrimRight(c.BaseURL, "/") + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, false, err
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = res.Body.Close() }()
	var out struct {
		Host struct {
			MemAvailablePercent float64 `json:"memAvailablePercent"`
		} `json:"host"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return 0, false, err
	}
	if out.Host.MemAvailablePercent == 0 {
		return 0, false, nil
	}
	return out.Host.MemAvailablePercent, true, nil
}
