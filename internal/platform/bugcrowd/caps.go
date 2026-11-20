package bugcrowd

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
)

// SubmissionCaps is what Bugcrowd allows this account to submit, read from the
// engagement's submission page.
//
// It is account-wide, not per engagement - "accounts are currently capped at
// six submissions per 7-day period" - but the page is where Bugcrowd renders it,
// inside a "submissionThrottleCaps" object in the HTML. There is no API for it:
// the researcher API's own submission endpoint only fails once the cap is hit.
type SubmissionCaps struct {
	Reason          string  `json:"reason"`
	WeeklyCap       int     `json:"weeklyCap"`
	WeeklyRemaining int     `json:"weeklyRemaining"`
	AtCap           bool    `json:"atCap"`
	AcceptanceRate  float64 `json:"acceptanceRate"`
	Threshold       float64 `json:"threshold"`
	EvaluatedCount  int     `json:"evaluatedCount"`
	MinEvaluated    int     `json:"minEvaluated"`
	WindowDays      int     `json:"evaluationWindowDays"`
	EarliestLiftAt  string  `json:"earliestLiftAt"`
}

// SubmissionCaps reads the caps from one engagement's submission page. The
// handle only has to exist and be visible to this account: the numbers are the
// account's.
func (c *Client) SubmissionCaps(ctx context.Context, handle string) (SubmissionCaps, error) {
	var caps SubmissionCaps
	if handle == "" {
		return caps, fmt.Errorf("bugcrowd: submission caps need an engagement handle")
	}
	u := c.base.ResolveReference(&url.URL{Path: "/engagements/" + handle + "/submissions/new"}).String()
	res, err := c.get(ctx, u, "text/html")
	if err != nil {
		return caps, err
	}
	if res.StatusCode != http.StatusOK {
		return caps, fmt.Errorf("bugcrowd: submission page: HTTP %d", res.StatusCode)
	}
	return ParseSubmissionCaps(string(res.Body))
}

// ParseSubmissionCaps finds the caps object in a submission page. It is separate
// from the request so a fixture can prove the shape: Bugcrowd writes the object
// into an attribute, so every quote in it arrives as &quot;.
func ParseSubmissionCaps(page string) (SubmissionCaps, error) {
	var caps SubmissionCaps
	page = html.UnescapeString(page)
	const key = `"submissionThrottleCaps":`
	i := strings.Index(page, key)
	if i < 0 {
		return caps, fmt.Errorf("bugcrowd: no submissionThrottleCaps on the page")
	}
	rest := page[i+len(key):]
	start := strings.IndexByte(rest, '{')
	if start < 0 {
		return caps, fmt.Errorf("bugcrowd: submissionThrottleCaps is not an object")
	}
	depth := 0
	end := -1
	for j := start; j < len(rest); j++ {
		switch rest[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = j + 1
			}
		}
		if end > 0 {
			break
		}
	}
	if end < 0 {
		return caps, fmt.Errorf("bugcrowd: submissionThrottleCaps is not closed")
	}
	if err := json.Unmarshal([]byte(rest[start:end]), &caps); err != nil {
		return caps, fmt.Errorf("bugcrowd: submission caps: %w", err)
	}
	return caps, nil
}
