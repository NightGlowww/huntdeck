package web

import (
	"encoding/xml"
	"net/http"
	"strings"
	"testing"
)

// Changes come as Atom feeds: all of them with the Changes page's
// filters, or one program's, linked from the pages.
func TestFeeds(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	read := func(path string) atomFeed {
		t.Helper()
		res, body := e.do(c, http.MethodGet, path, nil, nil)
		if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/atom+xml") {
			t.Fatalf("%s = %d %s", path, res.StatusCode, res.Header.Get("Content-Type"))
		}
		var f atomFeed
		if err := xml.Unmarshal([]byte(body), &f); err != nil {
			t.Fatalf("%s: %v\n%s", path, err, body)
		}
		return f
	}
	all := read("/changes.atom")
	if len(all.Entries) == 0 || all.Title != "HuntDeck: changes" || !strings.HasPrefix(all.Entries[0].ID, "http://127.0.0.1:") {
		t.Fatalf("feed = %+v", all)
	}
	scope := read("/changes.atom?group=scope&scope=added&since=24h")
	for _, en := range scope.Entries {
		if en.Category.Term != "scope_added" {
			t.Errorf("scope-added feed has %s", en.Category.Term)
		}
	}
	if len(scope.Entries) == 0 || scope.Links[1].Href[strings.Index(scope.Links[1].Href, "/changes"):] != "/changes?group=scope&scope=added" {
		t.Errorf("scope feed: %d entries, page %q", len(scope.Entries), scope.Links[1].Href)
	}
	acme := read("/programs/fake/acme/changes.atom")
	if len(acme.Entries) == 0 || !strings.HasPrefix(acme.Entries[0].Title, "Acme Corp: ") || acme.Title != "HuntDeck: Acme Corp (Fake)" {
		t.Errorf("program feed = %+v", acme)
	}
	if res, _ := e.do(c, http.MethodGet, "/programs/fake/nope/changes.atom", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("missing program's feed = %d", res.StatusCode)
	}
	for path, want := range map[string]string{
		"/changes?group=scope&bounty=1":   `<link rel="alternate" type="application/atom+xml" title="HuntDeck: changes" href="/changes.atom?bounty=1&amp;group=scope">`,
		"/programs/fake/acme?tab=changes": `href="/programs/fake/acme/changes.atom" data-export`,
	} {
		if _, body := e.do(c, http.MethodGet, path, nil, nil); !strings.Contains(body, want) {
			t.Errorf("%s lacks %s", path, want)
		}
	}
}
