package huntmail

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// Blocks are HuntMail's rules for mail it turns away on arrival (1.3 and
// later; Stats.Blocked says a version has them). HuntMail keeps and applies
// them; HuntDeck only lists, adds and removes them. Blocked mail is refused
// before it's read, so the sender gets a bounce and nothing is stored; mail
// already stored stays.

// Block kinds.
const (
	// BlockSender: mail whose From header is on a domain or under it
	// (shop.example takes news.shop.example, not shopx.example).
	BlockSender = "sender"
	// BlockRecipient: mail to one address, exactly.
	BlockRecipient = "recipient"
	// BlockPrefix: mail to addresses starting with the value.
	BlockPrefix = "prefix"
)

// Block scopes. A sender block takes either; the others take BlockAll.
const (
	// BlockNewsletters: only the sender's mail that looks like a newsletter
	// on arrival (list headers, a subject that isn't account mail).
	BlockNewsletters = "newsletters"
	// BlockAll: all of it.
	BlockAll = "all"
)

// Block is one of HuntMail's blocks.
type Block struct {
	ID        int64      `json:"id"`
	Kind      string     `json:"kind"`
	Value     string     `json:"value"`
	Scope     string     `json:"scope"`
	CreatedAt time.Time  `json:"created_at"`
	Hits      int64      `json:"hits"` // mail it turned away
	LastHit   *time.Time `json:"last_hit"`
}

// NewBlock is a block to add. Adding one that exists (the same kind and
// value) changes its scope instead.
type NewBlock struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	Scope string `json:"scope,omitempty"`
}

// Blocks lists HuntMail's blocks, newest first.
func (c *Client) Blocks(ctx context.Context) ([]Block, error) {
	var out struct {
		Blocks []Block `json:"blocks"`
	}
	err := c.getJSON(ctx, "list blocks", "/api/blocks", nil, &out)
	return out.Blocks, err
}

// AddBlock adds a block, or sets an existing one's scope. HuntMail checks
// the value: a refusal is an *APIError whose Text says why.
func (c *Client) AddBlock(ctx context.Context, b NewBlock) (Block, error) {
	var out Block
	err := c.callJSON(ctx, "add a block", http.MethodPost, "/api/blocks", nil, b, &out)
	return out, err
}

// DeleteBlock removes a block; ErrNotFound when there's no such block.
func (c *Client) DeleteBlock(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrNotFound
	}
	return c.callJSON(ctx, "remove a block", http.MethodDelete, "/api/blocks/"+strconv.FormatInt(id, 10), nil, nil, nil)
}
