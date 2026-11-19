package web

import (
	"net/http"
	"strings"
	"testing"
)

// A "3d ago" that cannot be recomputed is a page that lies while it is open:
// the ticker only rewrites times it finds by data-at, on the element or on an
// ancestor. Every .when span therefore needs one behind it - either on itself
// or on the wrapper that opened just before it.
func TestBoardRelativeTimesTick(t *testing.T) {
	e := newTestEnv(t)
	res, body := e.do(e.client(), http.MethodGet, "/", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/: %d", res.StatusCode)
	}
	if !strings.Contains(body, "data-at=") {
		t.Skip("this fixture has no times to tick")
	}
	// A sibling data-at would satisfy a "look back N bytes" check while the
	// ticker, which walks elements, would never find it: the test has to read the
	// markup as markup. Each .when must carry data-at itself, or sit inside the
	// nearest element that opened before it - which is its parent.
	checked := 0
	for i := 0; ; {
		j := strings.Index(body[i:], "class=\"when")
		if j < 0 {
			break
		}
		j += i
		end := strings.Index(body[j:], ">")
		if end < 0 {
			break
		}
		tag := body[j : j+end]
		if !strings.Contains(tag, "data-at=") && !parentHasDataAt(body, j) {
			t.Errorf("a .when span cannot tick: %s", tag)
		}
		checked++
		i = j + end
	}
	if checked == 0 {
		t.Fatal("no .when spans were checked: the guard is looking at nothing")
	}
	t.Logf("checked %d .when spans", checked)
}

// parentHasDataAt reports whether the element containing the span that starts at
// pos carries data-at. The ticker walks elements, so a sibling with data-at is
// not enough: the test has to read the markup as markup, not as nearby bytes.
func parentHasDataAt(body string, pos int) bool {
	depth := 0
	for i := pos - 1; i >= 0; i-- {
		if body[i] != '>' {
			continue
		}
		open := strings.LastIndex(body[:i], "<")
		if open < 0 {
			return false
		}
		tag := body[open : i+1]
		switch {
		case strings.HasPrefix(tag, "</"):
			depth++
		case isVoidTag(tag):
			continue
		case depth > 0:
			depth--
		default:
			return strings.Contains(tag, "data-at=")
		}
	}
	return false
}

func isVoidTag(tag string) bool {
	for _, v := range []string{"<br", "<img", "<input", "<hr", "<meta", "<link", "<use", "<path", "<col"} {
		if strings.HasPrefix(tag, v) {
			return true
		}
	}
	return false
}
