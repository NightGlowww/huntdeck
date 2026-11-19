package web

import "testing"

// The chip grid depends on this parse; a double-escaped \\w once left it
// matching nothing and the page fell back to the text wall it replaced.
func TestParsePojiaClasses(t *testing.T) {
	in := "xss — 跨站脚本 (Cross-site Scripting): 2,498 reports, 10 weaknesses\ncsrf — 跨站请求伪造: 519 reports"
	got := parsePojiaClasses(in)
	if len(got) != 2 {
		t.Fatalf("parsed %d entries, want 2", len(got))
	}
	if got[0].Key != "xss" || got[0].Count != 2498 || got[1].Key != "csrf" || got[1].Count != 519 {
		t.Errorf("parsed %+v", got)
	}
}
