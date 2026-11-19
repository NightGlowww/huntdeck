package web

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Policies are not ASCII: capitalising or slicing by byte broke the rune.
func TestPolicyTextHelpersHandleNonASCII(t *testing.T) {
	if got := upperFirst("éclair"); got != "Éclair" {
		t.Errorf("upperFirst: %q", got)
	}
	if !utf8.ValidString(upperFirst("éclair")) {
		t.Error("upperFirst produced invalid UTF-8")
	}
	text := "Le programme interdit le déni de service. Rapports dans 30 jours."
	ex := excerptAround(text, "déni de service")
	if !strings.Contains(ex, "déni de service") || !utf8.ValidString(ex) {
		t.Errorf("excerptAround: %q", ex)
	}
}
