package intigriti

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// The researcher's own state on the platform. Two sources: the account and the
// queue live in the web app's API (the researcher API has no notion of "me"),
// and the money lives in the researcher API's /v1/payouts.

// Account is who the token belongs to: reputation, rank and whether the
// identity check is done. Discovered live from /api/user/user, which sits
// outside the /api/core/researcher prefix the rest of the web calls use.
type Account struct {
	UserName         string   "json:\"userName\""
	UserID           string   "json:\"userId\""
	Reputation       int      "json:\"reputation\""
	Rank             int      "json:\"rank\""
	IdentityChecked  int      "json:\"identityChecked\""
	IdentityAttempts int      "json:\"identityCheckAttempts\""
	SanctionCleared  bool     "json:\"sanctionCleared\""
	Country          string   "json:\"country\""
	City             string   "json:\"city\""
	Skills           []string "json:\"skills\""
	ProfileCompleted bool     "json:\"profileCompleted\""
	Roles            []string "json:\"roles\""
}

// Verified reports whether the identity check is done. The platform answers with
// a state number rather than a flag; 0 means not started and anything higher is
// past that, so the caller reads this as "submitted or approved", not as "paid".
func (a Account) Verified() bool { return a.IdentityChecked > 0 }

// MyAccount reads the researcher's own state. (Account, without the ctx, is
// the platform's account NAMER, implemented above.) Requires the web session: the
// researcher API has no endpoint for it.
func (c *Client) MyAccount(ctx context.Context) (Account, error) {
	if c.web == nil {
		return Account{}, fmt.Errorf("intigriti: account: %w", ErrNoWebSession)
	}
	if err := c.ensureSession(ctx, false); err != nil {
		return Account{}, err
	}
	var a Account
	u := c.web.app.String() + "/api/user/user"
	if err := c.webGetAccount(ctx, u, &a); err != nil {
		return Account{}, fmt.Errorf("intigriti: account: %w", err)
	}
	return a, nil
}

// webGetAccount is webJSON with a full path instead of one under the researcher
// prefix, because /api/user/user is served by a different controller.
func (c *Client) webGetAccount(ctx context.Context, u string, into any) error {
	res, err := c.webDo(ctx, http.MethodGet, u, nil, "")
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if !strings.Contains(res.Header.Get("Content-Type"), "json") {
		return fmt.Errorf("not JSON")
	}
	return json.NewDecoder(bytes.NewReader(res.Body)).Decode(into)
}

// Invite is a private program invitation waiting for an answer.
type Invite struct {
	ProgramID string "json:\"programId\""
	Company   string "json:\"companyHandle\""
	Handle    string "json:\"handle\""
}

// Invites lists the invitations this researcher has not answered yet. An empty
// answer is the normal state for a new account.
func (c *Client) Invites(ctx context.Context) ([]Invite, error) {
	var out []Invite
	if err := c.webJSON(ctx, http.MethodGet, "/invites", nil, &out); err != nil {
		return nil, fmt.Errorf("intigriti: invites: %w", err)
	}
	return out, nil
}

// Application is a request to join a program that decides before you can hunt.
type Application struct {
	ProgramID string "json:\"programId\""
	Company   string "json:\"companyHandle\""
	Handle    string "json:\"handle\""
	Status    string "json:\"status\""
}

// Applications lists the applications this researcher has open.
func (c *Client) Applications(ctx context.Context) ([]Application, error) {
	var out []Application
	if err := c.webJSON(ctx, http.MethodGet, "/applications", nil, &out); err != nil {
		return nil, fmt.Errorf("intigriti: applications: %w", err)
	}
	return out, nil
}

// Payout is one bounty payment from the researcher API's BETA endpoint.
type Payout struct {
	ID     string "json:\"id\""
	Amount struct {
		Value    float64 "json:\"value\""
		Currency string  "json:\"currency\""
	} "json:\"amount\""
	Status struct {
		ID    int    "json:\"id\""
		Value string "json:\"value\""
	} "json:\"status\""
	CreatedAt string "json:\"createdAt\""
	PaidAt    string "json:\"paidAt\""
}

// Payouts lists every payment this token can see, newest first as the API
// returns them. It is marked BETA by the platform, which is why the page treats
// an error here as "not available" rather than as a fault.
func (c *Client) Payouts(ctx context.Context) ([]Payout, error) {
	var page struct {
		MaxCount int      "json:\"maxCount\""
		Records  []Payout "json:\"records\""
	}
	u := c.base.String() + "/v1/payouts?limit=100&offset=0"
	if err := c.getJSON(ctx, u, &page); err != nil {
		return nil, fmt.Errorf("intigriti: payouts: %w", err)
	}
	return page.Records, nil
}

// ErrNoWebSession says a call needs the web login and there is none.
var ErrNoWebSession = fmt.Errorf("no web session: log in on the Settings page")

// ensureURL is a compile-time reminder that paths are joined by the caller.
var _ = url.PathEscape
