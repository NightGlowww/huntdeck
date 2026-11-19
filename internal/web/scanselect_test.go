package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/config"
)

// A select's options are the one place html/template's escaping cannot save a
// missing close tag: browsers forgive it, so the page looks right while the
// markup is wrong and every strict parser sees nested options. The scanner is
// faked here on purpose - an empty list would make the count 0 == 0, which is
// the guard that cannot fail.
func TestScanSelectsCloseTheirOptions(t *testing.T) {
	scanner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/healthz") {
			_, _ = w.Write([]byte("{\"host\":{\"memAvailablePercent\":68}}"))
			return
		}
		var req struct {
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		answer := "{}"
		switch req.Params.Name {
		case "list_scan_templates":
			answer = "{\"list\":[{\"id\":\"tpl-1\",\"name\":\"fake-template\"}]}"
		case "list_nodes":
			answer = "{\"list\":[{\"name\":\"fake-node\",\"state\":\"1\",\"version\":\"9.9\"}]}"
		}
		payload, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{"content": []map[string]string{{"type": "text", "text": answer}}},
		})
		_, _ = w.Write(payload)
	}))
	defer scanner.Close()

	e := newTestEnv(t, func(d *Deps) {
		d.ScanURL = scanner.URL
		d.ScanKey = config.Secret("ssk_test")
	})
	res, body := e.do(e.client(), http.MethodGet, "/scan", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/scan: %d", res.StatusCode)
	}
	if !strings.Contains(body, "fake-template") || !strings.Contains(body, "fake-node") {
		t.Fatalf("the selects did not render the scanner's lists")
	}
	if got, want := strings.Count(body, "<option"), strings.Count(body, "</option>"); got != want {
		t.Errorf("%d <option> but %d </option>", got, want)
	}
	// The fake scanner offers one template and one node, so the page has two
	// selects with a placeholder each and one choice apiece. Only counting the
	// options, and requiring more than the two placeholders, is what catches a
	// list degrading to plain spans: the names stay in the body either way.
	if got := strings.Count(body, "<option"); got != 4 {
		t.Errorf("%d options in all, want 4 (2 placeholders + 1 template + 1 node)", got)
	}
}
