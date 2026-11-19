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
	const window = 200
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
		from := j - window
		if from < 0 {
			from = 0
		}
		if !strings.Contains(tag, "data-at=") && !strings.Contains(body[from:j], "data-at=") {
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
