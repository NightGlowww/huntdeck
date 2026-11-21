package web

import (
	"net/http"
	"strings"
	"testing"
)

func TestSystemAndIntegrationsWorkbenches(t *testing.T) {
	e := newTestEnv(t)
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"/system", []string{"System workbench", "Polling", "Accounts", "Notifications", "Interface", "Scoring"}},
		{"/integrations", []string{"Integrations workbench", "API", "MCP", "POJIA", "Scan handoff", "AI policy extraction"}},
	} {
		res, body := e.do(e.client(), http.MethodGet, tc.path, nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", tc.path, res.StatusCode)
		}
		for _, want := range tc.want {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %q", tc.path, want)
			}
		}
		for _, link := range []string{`href="/system"`, `href="/integrations"`} {
			if !strings.Contains(body, link) {
				t.Errorf("%s: missing navigation link %q", tc.path, link)
			}
		}
	}
}
