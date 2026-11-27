package web

import (
	"io/fs"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestPaletteContrast keeps every palette readable: the interface's text
// colours must reach WCAG AA (4.5:1) on the surfaces they sit on, in light
// and dark alike. The palettes live in static/css/00-theme.css, so the test
// reads them from there.
func TestPaletteContrast(t *testing.T) {
	css, err := fs.ReadFile(staticFS, "static/css/00-theme.css")
	if err != nil {
		t.Fatal(err)
	}
	palettes := map[string]map[string][2]string{} // palette -> var -> {light, dark}
	block := regexp.MustCompile(`(?s)(?::root, )?\[data-palette="(\w+)"\] \{(.*?)\n\}`)
	varDef := regexp.MustCompile(`--([\w-]+):\s*light-dark\((#[0-9a-fA-F]{6}), (#[0-9a-fA-F]{6})\)`)
	for _, m := range block.FindAllStringSubmatch(string(css), -1) {
		vars := map[string][2]string{}
		for _, v := range varDef.FindAllStringSubmatch(m[2], -1) {
			vars[v[1]] = [2]string{strings.ToLower(v[2]), strings.ToLower(v[3])}
		}
		if len(vars) > 0 {
			palettes[m[1]] = vars
		}
	}
	if len(palettes) == 0 {
		t.Fatal("no palettes found in 00-theme.css")
	}
	pairs := [][2]string{
		{"text", "surface"}, {"text", "bg"}, {"text-2", "surface"},
		{"muted", "surface"}, {"faint", "surface"}, {"accent", "surface"},
		{"on-accent", "accent"}, {"good", "surface"}, {"info", "surface"},
		{"warn", "surface"}, {"bad", "surface"}, {"priv", "surface"},
		{"pf-vulbox", "surface"}, {"pf-vulbox", "bg"},
		{"pf-issuehunt", "surface"}, {"pf-issuehunt", "bg"},
		{"pf-gobugfree", "surface"}, {"pf-gobugfree", "bg"},
		{"pf-inspectiv", "surface"}, {"pf-inspectiv", "bg"},
	}
	for name, vars := range palettes {
		borderInput, okBorder := vars["border-input"]
		if !okBorder {
			t.Errorf("%s: missing --border-input", name)
		}
		for mode, idx := range []int{0, 1} {
			for _, p := range pairs {
				fg, okF := vars[p[0]]
				bg, okB := vars[p[1]]
				if !okF || !okB {
					continue
				}
				if got := contrastRatio(fg[idx], bg[idx]); got < 4.5 {
					t.Errorf("%s (%s): %s on %s is %.2f:1, want 4.5", name, []string{"light", "dark"}[mode], p[0], p[1], got)
				}
			}
			if okBorder {
				for _, bgName := range []string{"surface", "bg"} {
					bg, okB := vars[bgName]
					if !okB {
						continue
					}
					if got := contrastRatio(borderInput[idx], bg[idx]); got < 3.0 {
						t.Errorf("%s (%s): border-input on %s is %.2f:1, want 3.0", name, []string{"light", "dark"}[mode], bgName, got)
					}
				}
			}
		}
	}
}

// contrastRatio is WCAG 2's ratio for two "#rrggbb" colours.
func contrastRatio(a, b string) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func relativeLuminance(hex string) float64 {
	hex = strings.TrimPrefix(hex, "#")
	channel := func(i int) float64 {
		v, _ := strconv.ParseUint(hex[i:i+2], 16, 8)
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(0) + 0.7152*channel(2) + 0.0722*channel(4)
}
