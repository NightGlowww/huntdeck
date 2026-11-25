package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestNoTemplateTextOutsideDefine guards a silent failure that shipped: a page's
// markup can sit outside its {{define}} blocks, and Go executes a named template
// happily while that text is never rendered. scan.html's "Handed over lately"
// panel spent its whole life there - one {{end}} too many closed the define,
// nothing failed, and the panel simply was not on the page.
//
// The rule: everything in a template file is inside a define. Only whitespace,
// comments and the closing {{end}}s may sit at depth zero.
func TestNoTemplateTextOutsideDefine(t *testing.T) {
	var files []string
	err := fs.WalkDir(templateFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".html") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 40 {
		t.Fatalf("only %d templates: the walk stopped matching", len(files))
	}
	// Two files are whole documents rather than named blocks: the layout every
	// page is executed with, and the login page before one exists. Their markup
	// is the point, not a stray.
	wholeDocument := map[string]bool{"templates/layout.html": true, "templates/login.html": true}
	var bad []string
	for _, name := range files {
		body, err := fs.ReadFile(templateFS, name)
		if err != nil {
			t.Fatal(err)
		}
		if wholeDocument[name] {
			continue
		}
		if line, stray := textOutsideDefine(string(body)); stray != "" {
			bad = append(bad, name+":"+numToStr(line)+": "+stray)
		}
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Errorf("%s is outside every {{define}}: a template never executes it", b)
	}
}

var templateAction = regexp.MustCompile(`(?s)\{\{.*?\}\}`)

// textOutsideDefine returns the first line holding text where no define is open,
// and that text. Every action opens or closes a block: {{if}}, {{range}},
// {{with}}, {{define}} and {{block}} open one, {{end}} closes one.
func textOutsideDefine(s string) (int, string) {
	var stack []string
	line := 1
	inDefine := func() bool {
		for _, k := range stack {
			if k == "define" {
				return true
			}
		}
		return false
	}
	for _, seg := range splitKeepingActions(s) {
		if !seg.isAction {
			if !inDefine() {
				if t := strings.TrimSpace(seg.text); t != "" {
					return line, firstStrayLine(t)
				}
			}
			line += strings.Count(seg.text, "\n")
			continue
		}
		a := seg.text
		line += strings.Count(a, "\n")
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(a, "{{"), "}}"))
		inner = strings.TrimSpace(strings.TrimPrefix(inner, "-"))
		switch {
		case strings.HasPrefix(inner, "/*"):
			continue
		case inner == "end":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case strings.HasPrefix(inner, "define "), strings.HasPrefix(inner, "block "):
			stack = append(stack, "define")
		case isOpenAction(inner):
			stack = append(stack, "other")
		default:
			if !inDefine() {
				return line, a
			}
		}
	}
	return 0, ""
}

// isOpenAction says whether an action opens a block that an {{end}} closes.
func isOpenAction(inner string) bool {
	for _, p := range []string{"if ", "range ", "with ", "if(", "range("} {
		if strings.HasPrefix(inner, p) {
			return true
		}
	}
	return false
}

type tmplSeg struct {
	text     string
	isAction bool
}

func splitKeepingActions(s string) []tmplSeg {
	var out []tmplSeg
	for {
		i := strings.Index(s, "{{")
		if i < 0 {
			out = append(out, tmplSeg{text: s})
			return out
		}
		if i > 0 {
			out = append(out, tmplSeg{text: s[:i]})
		}
		j := actionEnd(s[i:])
		if j < 0 {
			out = append(out, tmplSeg{text: s[i:], isAction: true})
			return out
		}
		out = append(out, tmplSeg{text: s[i : i+j], isAction: true})
		s = s[i+j:]
	}
}

// actionEnd finds the }} that closes an action, skipping any inside a quoted
// string: a translated string may hold template code as its text (the DKIM
// tooltip does), and cutting at its inner }} unbalanced everything after it.
func actionEnd(s string) int {
	inQuote := byte(0)
	for i := 2; i < len(s)-1; i++ {
		c := s[i]
		switch {
		case inQuote != 0:
			if c == '\\' {
				i++
				continue
			}
			if c == inQuote {
				inQuote = 0
			}
		case c == '"' || c == '`':
			inQuote = c
		case c == '}' && s[i+1] == '}':
			return i + 2
		}
	}
	return -1
}

func firstStrayLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 60 {
		s = s[:60] + "…"
	}
	return s
}

func numToStr(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

var _ = os.Getenv
var _ = filepath.Join
