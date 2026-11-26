package web

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestSettingsUX(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()

	t.Run("interval hints in table cells", func(t *testing.T) {
		res, body := e.do(c, http.MethodGet, "/settings", nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET /settings: %d", res.StatusCode)
		}
		doc, err := html.Parse(strings.NewReader(body))
		if err != nil {
			t.Fatalf("parse /settings HTML: %v", err)
		}

		var hints []*html.Node
		var inputs []*html.Node
		walkHTML(doc, func(n *html.Node) {
			if n.Type != html.ElementNode {
				return
			}
			if hasClass(n, "ival-hint") {
				hints = append(hints, n)
			}
			if n.Data == "input" && hasClass(n, "interval") {
				inputs = append(inputs, n)
			}
		})

		if len(inputs) < 3 {
			t.Fatalf("found %d input.interval elements, want at least 3 (quick, full, web_interval)", len(inputs))
		}
		if len(hints) == 0 {
			t.Fatal("found 0 .ival-hint elements")
		}
		for _, h := range hints {
			if !hasAncestor(h, "td") {
				t.Errorf(".ival-hint found outside <td> (parent=%q)", nodeTag(h.Parent))
			}
		}
		for _, in := range inputs {
			parent := in.Parent
			if parent == nil {
				t.Errorf("input.interval %q has nil parent", attrVal(in, "name"))
				continue
			}
			if !hasClass(parent, "ival-box") {
				t.Errorf("input.interval %q parent has class %q, want .ival-box", attrVal(in, "name"), attrVal(parent, "class"))
			}
			var siblingHints int
			walkHTML(parent, func(n *html.Node) {
				if n.Type == html.ElementNode && hasClass(n, "ival-hint") {
					siblingHints++
				}
			})
			if siblingHints != 1 {
				t.Errorf("input.interval %q parent has %d .ival-hint elements, want 1", attrVal(in, "name"), siblingHints)
			}
		}
	})

	t.Run("reports tab spacing around gone quiet", func(t *testing.T) {
		res, body := e.do(c, http.MethodGet, "/settings?tab=reports", nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET /settings?tab=reports: %d", res.StatusCode)
		}
		if !strings.Contains(body, " <b>gone quiet</b> ") {
			t.Error("settings?tab=reports lacks spaces around <b>gone quiet</b>")
		}
		if !strings.Contains(body, " <code>gone_quiet</code> ") {
			t.Error("settings?tab=reports lacks spaces around <code>gone_quiet</code>")
		}
	})

	t.Run("single h1 on settings and notifications", func(t *testing.T) {
		for _, path := range []string{"/settings", "/settings/notifications"} {
			res, body := e.do(c, http.MethodGet, path, nil, nil)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("GET %s: %d", path, res.StatusCode)
			}
			if got := strings.Count(body, "<h1"); got != 1 {
				t.Errorf("%s rendered %d <h1 elements, want 1", path, got)
			}
			if !strings.Contains(body, `<nav class="trail"`) {
				t.Errorf("%s lacks topbar .trail navigation", path)
			}
		}
	})
}

func walkHTML(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkHTML(c, fn)
	}
}

func hasAncestor(n *html.Node, tag string) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && p.Data == tag {
			return true
		}
	}
	return false
}

func attrVal(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, cls string) bool {
	return slices.Contains(strings.Fields(attrVal(n, "class")), cls)
}

func nodeTag(n *html.Node) string {
	if n == nil {
		return ""
	}
	return n.Data
}
