package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A program's notes are kept by HuntDeck now (program_notes), so they follow
// the user to another browser: the tab is a form, and saving shows the text
// back on the page.
func TestNotes(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	_, body := e.do(c, http.MethodGet, "/programs/fake/acme?tab=notes", nil, nil)
	for _, want := range []string{`<form method="post" action="/programs/fake/acme/note">`,
		`<textarea id="notes-text" name="note"`, "Kept in HuntDeck", "Save note"} {
		if !strings.Contains(body, want) {
			t.Errorf("notes tab lacks %s", want)
		}
	}

	form := url.Values{"note": {"Try /api/v2 for the authz bypass."}}
	res, _ := e.do(c, http.MethodPost, "/programs/fake/acme/note", form, nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d", res.StatusCode)
	}
	_, body = e.do(c, http.MethodGet, "/programs/fake/acme?tab=notes", nil, nil)
	if !strings.Contains(body, "Try /api/v2 for the authz bypass.") {
		t.Error("the saved note is not on the page")
	}

	// Saving an empty note clears it.
	if res, _ := e.do(c, http.MethodPost, "/programs/fake/acme/note", url.Values{"note": {""}}, nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("clear: %d", res.StatusCode)
	}
	if _, body = e.do(c, http.MethodGet, "/programs/fake/acme?tab=notes", nil, nil); strings.Contains(body, "authz bypass") {
		t.Error("the cleared note is still there")
	}
}
