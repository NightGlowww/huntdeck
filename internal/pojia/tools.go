package pojia

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// SearchReports finds disclosed reports by keywords, class, program or severity.
// The answer is text, like every POJIA answer.
func (c *Client) SearchReports(ctx context.Context, query, class, program string, limit int) (string, error) {
	args := map[string]any{}
	if query != "" {
		args["query"] = query
	}
	if class != "" {
		args["class"] = class
	}
	if program != "" {
		args["program"] = program
	}
	if limit > 0 {
		args["limit"] = limit
	}
	return c.Call(ctx, "search_reports", args)
}

// GetReport is one report with its annotation: how it was found, the steps,
// the payloads, the fix. POJIA wants the id as a number, not a string.
func (c *Client) GetReport(ctx context.Context, id string) (string, error) {
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(id, "#")))
	if err != nil {
		return "", fmt.Errorf("pojia: report id %q is not a number", id)
	}
	return c.Call(ctx, "get_report", map[string]any{"id": n})
}

// GetPayloads is the curated test payloads and bypasses for one class.
func (c *Client) GetPayloads(ctx context.Context, class string) (string, error) {
	return c.Call(ctx, "get_payloads", map[string]any{"class": class})
}

// ListClasses is every vulnerability class with its report counts.
func (c *Client) ListClasses(ctx context.Context) (string, error) {
	return c.Call(ctx, "list_classes", map[string]any{})
}

// CVELookup is one CVE: score, CWE, KEV and linked reports.
func (c *Client) CVELookup(ctx context.Context, id string) (string, error) {
	return c.Call(ctx, "cve_lookup", map[string]any{"cve": id})
}

// SimilarReports finds historically similar disclosed reports for a feature
// description, in Chinese or English.
func (c *Client) SimilarReports(ctx context.Context, description string) (string, error) {
	return c.Call(ctx, "similar_reports", map[string]any{"description": description})
}

// ErrNotFound reports POJIA answering "not in the index".
func ErrNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "不存在") || strings.Contains(msg, "404") ||
		fmt.Sprintf("%v", err) == ""
}
