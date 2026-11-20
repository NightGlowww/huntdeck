package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The page dropped the candidate read's error and rendered "nothing waiting",
// which reads as a quiet day over a database that could not be read.
func TestScanPageSaysWhenTheCandidatesCannotBeRead(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	// A dropped table is how the helpers ask what a page does when something is
	// unreadable; closing the pool would also fail the request, differently.
	if _, err := e.db.W.ExecContext(context.Background(), `DROP TABLE scan_handoffs`); err != nil {
		t.Fatal(err)
	}
	res, body := e.do(c, "GET", "/scan", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("scan page: %d", res.StatusCode)
	}
	if !strings.Contains(body, "could not read the candidates") {
		t.Fatalf("the page hid a failed read:\n%s", body[:min(400, len(body))])
	}
}

// The page says nothing has moved only when it has not: two hours of waiting with
// targets queued is the state that took an evening to notice by hand.
func TestStallFor(t *testing.T) {
	now := time.Date(2026, 11, 21, 4, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		last    time.Time
		waiting int
		want    time.Duration
	}{
		{"moved a minute ago", now.Add(-time.Minute), 10, 0},
		{"moved ninety minutes ago", now.Add(-90 * time.Minute), 10, 0},
		{"stuck for three hours", now.Add(-3 * time.Hour), 10, 3 * time.Hour},
		{"stuck but nothing waiting", now.Add(-3 * time.Hour), 0, 0},
		{"never handed over", time.Time{}, 10, 0},
	}
	for _, c := range cases {
		if got := stallFor(c.last, now, c.waiting); got != c.want {
			t.Errorf("%s: stallFor = %s, want %s", c.name, got, c.want)
		}
	}
}
