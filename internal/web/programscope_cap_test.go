package web

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/mdpsec/huntdeck/internal/store"
)

// Even "show all" is a page: a program with thousands of entries is capped,
// and the page says the export carries the rest instead of offering a link that
// would change nothing.
func TestScopeShowAllIsCapped(t *testing.T) {
	entries := make([]store.EntryView, scopeRowsAll+10)
	for i := range entries {
		entries[i] = store.EntryView{Identifier: fmt.Sprintf("h%d.example.com", i), AssetType: "domain"}
	}
	d := &programData{InScope: entries}
	page := d.scopeView(httptest.NewRequest("GET", "/programs/fake/acme?tab=scope&all=1", nil))
	if len(page.Rows) != scopeRowsAll {
		t.Fatalf("capped view has %d rows, want %d", len(page.Rows), scopeRowsAll)
	}
	if !page.Truncated || !page.Limited {
		t.Errorf("truncated=%v limited=%v, want both true", page.Truncated, page.Limited)
	}
	if page.Total != len(entries) {
		t.Errorf("total %d, want %d", page.Total, len(entries))
	}
}
