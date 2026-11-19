package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Two counts the queue page used to render as zero when their read failed: the
// score history ("0 of 8 snapshots" is a claim about the data) and the number of
// targets the floor left out (its absence implies nothing was left out).
func TestQueueSaysWhenItsCountsCannotBeRead(t *testing.T) {
	e := newTestEnv(t)
	if err := e.store.RefreshHuntQueue(context.Background(), e.store.Now()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"hunt_scores", "queue_dropped"} {
		if _, err := e.db.W.ExecContext(context.Background(), "ALTER TABLE "+table+" RENAME TO "+table+"_gone"); err != nil {
			t.Fatal(err)
		}
	}
	res, body := e.do(e.client(), http.MethodGet, "/queue", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("queue: %d", res.StatusCode)
	}
	if !strings.Contains(body, "could not be read just now, so how many snapshots") {
		t.Error("the page does not say the history could not be read")
	}
	if strings.Contains(body, "0 of 8 snapshots") {
		t.Error("the page claims 0 snapshots after a failed read")
	}
	if !strings.Contains(body, "floor left out could not be read") {
		t.Error("the page does not say the left-out count could not be read")
	}
}
