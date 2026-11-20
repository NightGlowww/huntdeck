package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
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
