package web

import (
	"os"
	"strings"
	"testing"
)

// Table capability contracts keep the interface from regressing into a set of
// visually similar but operationally incomplete lists. Read-only tables are
// intentionally listed separately because selection and destructive actions
// do not belong on every table.
func TestMutableTableCapabilities(t *testing.T) {
	cases := []struct {
		file string
		want []string
	}{
		{"templates/programs.html", []string{"data-select-all", `class="pick"`, "data-selbar", "data-csv-cols", "colpick"}},
		{"templates/targets.html", []string{"data-select-all", `class="pick"`, "data-selbar", "data-copy-url", "data-export"}},
		{"templates/disclosed.html", []string{"data-select-all", `class="pick"`, "data-selbar", "data-export"}},
		{"templates/dashboard.html", []string{"data-select-all", `class="pick"`, "data-selbar", "data-copy-list"}},
		{"templates/api.html", []string{"data-select-all", `class="pick"`, "data-selbar", "/api/tokens/revoke-bulk", "/api/tokens/delete-bulk"}},
		{"templates/mcp.html", []string{"data-select-all", `class="pick"`, "data-selbar", "/mcp/delete"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			body, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatal(err)
			}
			text := string(body)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("missing table capability %q", want)
				}
			}
		})
	}
}

func TestReadOnlyTableCapabilities(t *testing.T) {
	cases := []struct {
		file string
		want []string
	}{
		{"templates/partials/program-scope.html", []string{"data-filter-rows", "data-copy-url", "data-export"}},
		{"templates/changes.html", []string{"data-export", "hasFilters"}},
		{"templates/queue.html", []string{"Start today", "Blocked"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			body, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatal(err)
			}
			text := string(body)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("missing read-only capability %q", want)
				}
			}
		})
	}
}
