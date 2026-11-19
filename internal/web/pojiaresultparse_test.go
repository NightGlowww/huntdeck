package web

import "testing"

// The structured search results depend on this parse; anything it misses
// falls back to the raw text, so the test pins the shape it must keep.
func TestParsePojiaResults(t *testing.T) {
	in := "#1339356 [none] Shopify Email XSS — Shopify · 跨站脚本 · https://hackerone.com/reports/1339356\n#1103298 [low] Parquet Viewer XSS — Shopify · 跨站脚本"
	got := parsePojiaResults(in)
	if len(got) != 2 {
		t.Fatalf("parsed %d, want 2", len(got))
	}
	if got[0].ID != "1339356" || got[0].Severity != "none" || got[0].Program != "Shopify" || got[0].URL == "" {
		t.Errorf("row 0: %+v", got[0])
	}
	if got[1].URL != "" {
		t.Errorf("row 1 should have no url: %+v", got[1])
	}
}
