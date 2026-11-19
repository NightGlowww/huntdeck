package store

import (
	"testing"
	"time"
)

// The Blocked list follows the page's filters: a platform filter that leaves out
// a program must leave out its gated targets too.
func TestGatedListRespectsTheFilter(t *testing.T) {
	q := newQueueTest(t)
	q.seed(t, queueSeedRows(time.Now()))
	all, err := q.s.HuntQueueGated(q.ctx, QueueFilter{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 {
		t.Skip("the fixture seeds no gated rows")
	}
	one := QueueFilter{Platform: all[0].Platform}
	filtered, err := q.s.HuntQueueGated(q.ctx, one, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range filtered {
		if r.Platform != one.Platform {
			t.Errorf("a %s row came back for a %s filter", r.Platform, one.Platform)
		}
	}
	if len(filtered) > len(all) {
		t.Errorf("filtering returned more rows (%d) than the unfiltered read (%d)", len(filtered), len(all))
	}
}
