package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/reports"
	"github.com/mdpsec/huntdeck/internal/store"
)

// TestOpenAPIDocumentsEveryRoute: every API route has an operation in
// openapi.json, with a summary, an id and a known tag, and the spec has no
// operation the server doesn't serve.
func TestOpenAPIDocumentsEveryRoute(t *testing.T) {
	o, err := loadOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, tg := range o.spec.Tags {
		tags = append(tags, tg.Name)
	}
	served := map[string]bool{}
	ids := map[string]bool{}
	pathParam := regexp.MustCompile(`\{(\w+)\}`)
	for _, rt := range (&server{}).apiRoutes() {
		method, path, ok := strings.Cut(rt.Pattern, " ")
		if !ok {
			t.Errorf("route %q has no method", rt.Pattern)
			continue
		}
		served[strings.ToLower(method)+" "+path] = true
		op := o.spec.Paths[path][strings.ToLower(method)]
		if op == nil {
			t.Errorf("%s isn't in openapi.json", rt.Pattern)
			continue
		}
		if op.Summary == "" || op.Description == "" || op.OperationID == "" {
			t.Errorf("%s: needs a summary, description and operationId", rt.Pattern)
		}
		if ids[op.OperationID] {
			t.Errorf("%s: operationId %q used twice", rt.Pattern, op.OperationID)
		}
		ids[op.OperationID] = true
		if len(op.Tags) == 0 || !slices.Contains(tags, op.Tags[0]) {
			t.Errorf("%s: tag %v isn't one of %v", rt.Pattern, op.Tags, tags)
		}
		for _, m := range pathParam.FindAllStringSubmatch(path, -1) {
			if !slices.ContainsFunc(op.Parameters, func(p oaParam) bool { return p.In == "path" && p.Name == m[1] && p.Required }) {
				t.Errorf("%s: path parameter %s isn't described as required", rt.Pattern, m[1])
			}
		}
		for code, res := range op.Responses {
			for ct, c := range res.Content {
				name, ok := strings.CutPrefix(c.Schema.Ref, "#/components/schemas/")
				if c.Schema.Ref != "" && (!ok || o.schemas()[name] == nil) {
					t.Errorf("%s %s %s: no schema %q", rt.Pattern, code, ct, c.Schema.Ref)
				}
			}
		}
	}
	for path, ops := range o.spec.Paths {
		for method := range ops {
			if !served[method+" "+path] {
				t.Errorf("openapi.json has %s %s, which isn't served", strings.ToUpper(method), path)
			}
		}
	}
}

// TestAPIResponsesFitSchemas sends requests covering each JSON response
// and checks the answers against the schemas the served spec gives them:
// every required key there, no key it doesn't know, types right (a list
// that comes back null where the schema says array fails).
func TestAPIResponsesFitSchemas(t *testing.T) {
	e := newTestEnv(t)
	e.addReports(t)
	c := e.client()
	res, body := e.do(c, "GET", "/api/v1/openapi.json", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("openapi.json = %d", res.StatusCode)
	}
	var spec struct {
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
		Components struct {
			Schemas map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal([]byte(body), &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Servers) != 1 || spec.Servers[0].URL != e.srv.URL {
		t.Errorf("servers = %+v, want %s", spec.Servers, e.srv.URL)
	}
	v := schemaCheck{defs: spec.Components.Schemas}

	tests := []struct {
		method, path, body string
		status             int
		schema             string
	}{
		{"GET", "/api/v1/board", "", 200, "Board"},
		{"GET", "/api/v1/reports", "", 200, "MyReportList"},
		{"GET", "/api/v1/reports?status=paid&sort=bounty&limit=1", "", 200, "MyReportList"},
		{"GET", "/api/v1/reports?status=nonsense", "", 400, "Error"},
		{"GET", "/api/v1/reports/analytics", "", 200, "ReportsAnalytics"},
		{"GET", "/api/v1/reports/analytics?from=2020-01&to=2020-02", "", 200, "ReportsAnalytics"},
		{"GET", "/api/v1/programs?state=all", "", 200, "ProgramList"},
		{"GET", "/api/v1/programs?sort=huntscore&limit=1", "", 200, "ProgramList"},
		{"GET", "/api/v1/programs?state=all&trends=1", "", 200, "ProgramList"},
		{"GET", "/api/v1/programs?q=nothing-matches", "", 200, "ProgramList"},
		{"GET", "/api/v1/programs/fake/acme", "", 200, "Program"},
		{"GET", "/api/v1/programs/fake/evil", "", 200, "Program"},
		{"GET", "/api/v1/programs/fake/missing", "", 404, "Error"},
		{"GET", "/api/v2/programs?state=all", "", 200, "ProgramListV2"},
		{"GET", "/api/v2/programs?state=all&trends=1&since=3650d", "", 200, "ProgramListV2"},
		{"GET", "/api/v2/programs?since=nonsense", "", 400, "Error"},
		{"GET", "/api/v2/invites", "", 200, "ProgramListV2"},
		{"GET", "/api/v2/programs/fake/acme", "", 200, "ProgramV2"},
		{"GET", "/api/v2/programs/fake/evil", "", 200, "ProgramV2"},
		{"GET", "/api/v2/programs/fake/missing", "", 404, "Error"},
		{"GET", "/api/v1/targets?kind=wildcards&format=json", "", 200, "TargetList"},
		{"GET", "/api/v1/targets?kind=domains&scope=out&format=json", "", 200, "TargetList"},
		{"GET", "/api/v1/targets?kind=roots&format=json", "", 200, "TargetList"},
		{"GET", "/api/v1/targets", "", 400, "Error"},
		{"GET", "/api/v1/match?host=shop.acme.example", "", 200, "HostMatch"},
		{"GET", "/api/v1/match?host=acme.example", "", 200, "HostMatch"},
		{"GET", "/api/v1/match?host=admin.acme.example", "", 200, "HostMatch"},
		{"GET", "/api/v1/match?host=nothing.invalid", "", 200, "HostMatch"},
		{"GET", "/api/v1/match", "", 400, "Error"},
		{"POST", "/api/v1/match", "shop.acme.example\nadmin.acme.example\n192.0.2.7\nnothing.invalid\n", 200, "BulkMatch"},
		{"GET", "/api/v1/changes?since=3650d", "", 200, "ChangeList"},
		{"GET", "/api/v1/changes?since=3650d&kind=scope_added", "", 200, "ChangeList"},
		{"GET", "/api/v1/changes?since=3650d&program=fake/acme", "", 200, "ChangeList"},
		{"GET", "/api/v1/changes?program=fake/missing", "", 404, "Error"},
		{"GET", "/api/v1/changes?program=acme", "", 404, "Error"},
		{"GET", "/api/v1/disclosed", "", 200, "DisclosedList"},
		{"GET", "/api/v1/disclosed?severity=critical", "", 200, "DisclosedList"},
		{"GET", "/api/v1/stats?state=all", "", 200, "StatsList"},
		{"GET", "/api/v1/stats/platforms", "", 200, "StatsOverview"},
		{"GET", "/api/v1/stats/platforms/fake", "", 200, "StatsPlatform"},
		{"GET", "/api/v1/stats/platforms/fake?sort=name&q=acme&limit=1", "", 200, "StatsPlatform"},
		{"GET", "/api/v1/stats/platforms/nowhere", "", 404, "Error"},
		{"GET", "/api/v1/status", "", 200, "Status"},
		{"POST", "/api/v1/poll/fake?mode=quick", "", 202, "OK"},
		{"GET", "/healthz", "", 200, "OK"},
	}
	for _, tt := range tests {
		req, err := http.NewRequestWithContext(context.Background(), tt.method, e.srv.URL+tt.path, strings.NewReader(tt.body))
		if err != nil {
			t.Fatal(err)
		}
		if tt.body != "" {
			req.Header.Set("Content-Type", "text/plain")
		}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != tt.status {
			t.Errorf("%s %s = %d, want %d: %s", tt.method, tt.path, res.StatusCode, tt.status, b)
			continue
		}
		var got any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Errorf("%s %s: not JSON: %v", tt.method, tt.path, err)
			continue
		}
		for _, problem := range v.check(map[string]any{"$ref": "#/components/schemas/" + tt.schema}, got, "$") {
			t.Errorf("%s %s: %s", tt.method, tt.path, problem)
		}
	}
}

// schemaCheck checks a JSON value against the generated schemas: the
// subset of JSON Schema schemaGen writes ($ref, anyOf, type, properties,
// required, items, additionalProperties). An object may hold no key its
// schema doesn't name.
type schemaCheck struct{ defs map[string]any }

func (v schemaCheck) check(s map[string]any, val any, at string) []string {
	if ref, ok := s["$ref"].(string); ok {
		def, _ := v.defs[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
		if def == nil {
			return []string{at + ": no schema " + ref}
		}
		return v.check(def, val, at)
	}
	if alts, ok := s["anyOf"].([]any); ok {
		var first []string
		for i, a := range alts {
			p := v.check(a.(map[string]any), val, at)
			if len(p) == 0 {
				return nil
			}
			if i == 0 {
				first = p
			}
		}
		return first
	}
	var types []string
	switch typ := s["type"].(type) {
	case string:
		types = []string{typ}
	case []any:
		for _, x := range typ {
			types = append(types, x.(string))
		}
	default:
		return nil // anything goes
	}
	got := jsonType(val)
	if !slices.Contains(types, got) && (got != "integer" || !slices.Contains(types, "number")) {
		return []string{fmt.Sprintf("%s: %s, want %s", at, got, strings.Join(types, " or "))}
	}
	var out []string
	switch got {
	case "object":
		obj := val.(map[string]any)
		props, _ := s["properties"].(map[string]any)
		add, _ := s["additionalProperties"].(map[string]any)
		for k, x := range obj {
			switch ps, ok := props[k].(map[string]any); {
			case ok:
				out = append(out, v.check(ps, x, at+"."+k)...)
			case add != nil:
				out = append(out, v.check(add, x, at+"."+k)...)
			default:
				out = append(out, at+"."+k+": not in the schema")
			}
		}
		req, _ := s["required"].([]any)
		for _, k := range req {
			if _, ok := obj[k.(string)]; !ok {
				out = append(out, fmt.Sprintf("%s: missing %s", at, k))
			}
		}
	case "array":
		items, _ := s["items"].(map[string]any)
		for i, x := range val.([]any) {
			out = append(out, v.check(items, x, fmt.Sprintf("%s[%d]", at, i))...)
		}
	case "string":
		if s["format"] == "date-time" {
			if _, err := time.Parse(time.RFC3339, val.(string)); err != nil {
				out = append(out, fmt.Sprintf("%s: %q isn't a date-time", at, val))
			}
		}
	}
	return out
}

func jsonType(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		if x == float64(int64(x)) {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

// TestOpenAPIValuesMatchCode: the values the spec lists for a parameter
// are the ones the code takes.
func TestOpenAPIValuesMatchCode(t *testing.T) {
	o, err := loadOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	values := func(path, name string) []string {
		for _, p := range o.spec.Paths[path]["get"].Parameters {
			if p.Name != name {
				continue
			}
			enum := p.Schema.Enum
			if p.Schema.Items != nil {
				enum = p.Schema.Items.Enum
			}
			var out []string
			for _, e := range enum {
				out = append(out, fmt.Sprint(e))
			}
			return out
		}
		t.Errorf("%s has no parameter %s", path, name)
		return nil
	}
	for _, tt := range []struct {
		path, param string
		code        []string
	}{
		{"/api/v1/programs", "sort", programSortKeys()},
		{"/api/v1/stats", "sort", store.StatsSorts},
		{"/api/v1/stats/platforms/{platform}", "sort", statsPlatformSorts},
		{"/api/v1/changes", "kind", store.ChangeKinds()},
		{"/api/v1/reports", "status", append(keysOf(reportGroups), "all")},
		{"/api/v1/reports", "state", keysOf(reports.States)},
		{"/api/v1/reports", "severity", append(keysOf(reports.Severities), "unrated")},
		{"/api/v1/reports", "platform", reportFormPlatforms},
		{"/api/v1/reports/analytics", "range", keysOf(analyticsRanges)},
	} {
		got, want := slices.Sorted(slices.Values(values(tt.path, tt.param))), slices.Sorted(slices.Values(tt.code))
		if !slices.Equal(got, want) {
			t.Errorf("%s %s: spec lists %v, code takes %v", tt.path, tt.param, got, want)
		}
	}
}

// TestAPIPage: the page lists every operation, and the old settings tab
// sends its links there.
func TestAPIPage(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	res, body := e.do(c, "GET", "/api", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/api = %d", res.StatusCode)
	}
	o, err := loadOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	for _, ops := range o.spec.Paths {
		for _, op := range ops {
			if !strings.Contains(body, `<section class="panel ep" id="`+op.OperationID+`">`) {
				t.Errorf("/api has no panel for %s", op.OperationID)
			}
		}
	}
	for _, want := range []string{`curl -s &#34;` + e.srv.URL + `/api/v1/programs?bounty=1&amp;sort=huntscore&amp;limit=5&#34;`,
		`value="/api/v1/programs/fake/acme"`, `<span class="mth post">POST</span><code>/api/v1/poll/{platform}</code>`} {
		if !strings.Contains(body, want) {
			t.Errorf("/api: missing %q", want)
		}
	}
	// Only GETs can be sent from the page.
	if n, m := strings.Count(body, "<form class=\"try\""), strings.Count(body, `<span class="mth get">GET</span><code>`); n != m {
		t.Errorf("/api: %d Send forms for %d GETs", n, m)
	}
	res, _ = e.do(c, "GET", "/settings?tab=api", nil, nil)
	if res.StatusCode != http.StatusMovedPermanently || res.Header.Get("Location") != "/api" {
		t.Errorf("/settings?tab=api = %d → %q, want 301 → /api", res.StatusCode, res.Header.Get("Location"))
	}
}

// keysOf lists the Key fields of a slice of choices.
func keysOf(s any) []string {
	v := reflect.ValueOf(s)
	out := make([]string, v.Len())
	for i := range out {
		out[i] = v.Index(i).FieldByName("Key").String()
	}
	return out
}
