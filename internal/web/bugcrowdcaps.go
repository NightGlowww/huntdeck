package web

import (
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform/bugcrowd"
)

// capsCache holds the Bugcrowd submission allowance for the polling row.
//
// The figure comes from a page on bugcrowd.com: fetching it while rendering made
// that page 1.6 seconds instead of 9 milliseconds, every time. So a render reads
// the cache and, when it is stale, starts one refresh in the background; the
// number appears on the next look rather than holding the page.
type capsCache struct {
	mu       sync.Mutex
	at       time.Time
	caps     bugcrowd.SubmissionCaps
	ok       bool
	inflight bool
}

// capsFreshFor is how long a cached allowance is trusted. The numbers move when
// a submission is made, which is not something to poll for.
const capsFreshFor = 10 * time.Minute

// get returns the cached caps and whether they are fresh.
func (c *capsCache) get(now time.Time) (bugcrowd.SubmissionCaps, bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caps, c.ok && now.Sub(c.at) < capsFreshFor, c.inflight
}

// set stores a result and clears the in-flight marker.
func (c *capsCache) set(caps bugcrowd.SubmissionCaps, ok bool, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.caps, c.ok, c.at, c.inflight = caps, ok, now, false
}

// startRefresh claims the single in-flight slot; false when one is running.
func (c *capsCache) startRefresh() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inflight {
		return false
	}
	c.inflight = true
	return true
}
