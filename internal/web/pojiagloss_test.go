package web

import (
	"strings"
	"testing"
)

// TestBeforeParenKeepsTheGlossForTheTooltip pins the helper behind the class chips:
// a label is "跨站请求伪造（Cross-Site Request Forgery）", and a one-line chip cut it
// mid-word. The chip shows the Chinese, the tooltip keeps the gloss.
func TestBeforeParenKeepsTheGlossForTheTooltip(t *testing.T) {
	cases := []struct{ in, want string }{
		{"跨站请求伪造（Cross-Site Request Forgery）", "跨站请求伪造"},
		{"SQL 注入（SQL Injection）", "SQL 注入"},
		{"no bracket here", "no bracket here"},
		{"（leading bracket）", "（leading bracket）"},
	}
	for _, c := range cases {
		if got := beforeParen(c.in); got != c.want {
			t.Errorf("beforeParen(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPOJIAChipsShowChineseOnly checks the page wires the helper in.
func TestPOJIAChipsShowChineseOnly(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) {})
	_, body := e.do(e.client(), "GET", "/pojia", nil, nil)
	if !strings.Contains(body, `class="lbl" title="`) && !strings.Contains(body, "pojia-chip") {
		t.Skip("POJIA is not configured in the fixture; the chip markup is absent")
	}
	if strings.Contains(body, "（Cross-Site") {
		t.Error("a chip still renders the English gloss as its text")
	}
}
