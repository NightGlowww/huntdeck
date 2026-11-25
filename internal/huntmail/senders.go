package huntmail

import (
	"context"
	"net/url"
	"strconv"
)

// Sender is one of HuntMail's top senders over a window: a site (the From
// domain's last two labels, or three under a country's second level), the
// value a sender block takes.
type Sender struct {
	Domain      string `json:"domain"`
	Messages    int64  `json:"messages"`
	Newsletters int64  `json:"newsletters"`
	Bytes       int64  `json:"bytes"`
	// Blocked is how much of its mail a sender block turns away:
	// BlockNewsletters or BlockAll, "" when none covers it.
	Blocked string `json:"blocked"`
	// BlockID is that block's (an "all" one over a "newsletters" one), to
	// unblock with DeleteBlock; nil with no block, or from a HuntMail that
	// doesn't say.
	BlockID *int64 `json:"block_id"`
}

// Senders lists the sites that sent the most mail over the last days
// (1–400), busiest first, at most limit (1–100). Mail blocks turned away
// isn't stored, so it doesn't count. A HuntMail before 1.6, without top
// senders, answers ErrNotFound. HuntMail reads every message in the
// window for it: ask when a page opens, not on a timer.
func (c *Client) Senders(ctx context.Context, days, limit int) ([]Sender, error) {
	q := url.Values{"days": {strconv.Itoa(min(max(days, 1), 400))}, "limit": {strconv.Itoa(min(max(limit, 1), 100))}}
	var out struct {
		Senders []Sender `json:"senders"`
	}
	err := c.getJSON(ctx, "list the top senders", "/api/senders", q, &out)
	return out.Senders, err
}
