package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Two reads whose failure used to look like emptiness. Neither could be tested
// before because the web package had no way to break a read; the test env now
// carries the database handle, so a dropped table is the fault.

// The board's queue strip was `{{if .Queue}}` and nothing else: a failed read and
// an empty queue both made it vanish, and a vanishing panel reads as "nothing
// worth doing today".
func TestBoardSaysWhenTheQueueCannotBeRead(t *testing.T) {
	e := newTestEnv(t)
	if _, err := e.db.W.ExecContext(context.Background(), "ALTER TABLE hunt_queue RENAME TO hunt_queue_gone"); err != nil {
		t.Fatal(err)
	}
	res, body := e.do(e.client(), http.MethodGet, "/", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("board: %d", res.StatusCode)
	}
	if !strings.Contains(body, "could not be read just now") {
		t.Error("the board does not say the queue could not be read")
	}
}

// A note that cannot be read must not open an empty editor: saving it would
// replace what is stored. The tab says so and stays closed.
func TestUnreadableNoteDoesNotOpenTheEditor(t *testing.T) {
	e := newTestEnv(t)
	if _, err := e.db.W.ExecContext(context.Background(), "ALTER TABLE program_notes RENAME TO program_notes_gone"); err != nil {
		t.Fatal(err)
	}
	res, body := e.do(e.client(), http.MethodGet, "/programs/fake/acme?tab=notes", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("program notes: %d", res.StatusCode)
	}
	if !strings.Contains(body, "editor stays closed") {
		t.Error("the notes tab does not explain why the editor is closed")
	}
	if strings.Contains(body, `id="notes-text"`) {
		t.Error("the editor opened even though the note could not be read")
	}
}
