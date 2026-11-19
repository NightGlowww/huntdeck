package web

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"strings"
	"time"
	"unicode"
)

// The API's OpenAPI 3.1 description, served at /api/v1/openapi.json and
// shown as the API page. openapi.json (embedded) holds the operations:
// paths, parameters, descriptions and which schema each response is. The
// schemas themselves are generated here from the Go types the handlers
// write (apitypes.go and the types they use), so they can't drift from
// what the API returns. A test checks every route is documented and real
// responses fit their schemas.

//go:embed openapi.json
var openapiJSON []byte

// openapi.zh.json holds the API page's Chinese text, keyed by the English
// it translates: the spec stays one machine-readable document, and the page
// looks its own words up (openapiZh).
//
//go:embed openapi.zh.json
var openapiZhJSON []byte

// apiSchemaRoots are the response bodies the operations refer to; their
// schema names come from schemaName ("apiProgramListJSON" is
// "ProgramList").
func apiSchemaRoots() []any {
	return []any{apiErrorJSON{}, apiOKJSON{}, apiStatusJSON{}, apiProgramListJSON{}, apiProgramJSON{},
		apiProgramListV2JSON{}, apiProgramV2JSON{}, apiStatsListJSON{},
		apiTargetListJSON{}, apiChangeListJSON{}, apiDisclosedListJSON{}, apiHostMatchJSON{}, apiBulkMatchJSON{},
		apiStatsOverviewJSON{}, apiStatsPlatformJSON{}, apiBoardJSON{}}
}

// openAPI is the spec, parsed once at startup.
type openAPI struct {
	doc  map[string]any // the whole spec, schemas included, as served
	spec oaSpec         // the parts the API page shows
	// order is each schema's properties in their fields' order, for
	// samples (the spec's are maps, so sorted).
	order map[string][]string
}

// oaSpec, oaOp and the rest are the parts of the spec the API page reads.
type oaSpec struct {
	Info struct {
		Title       string `json:"title"`
		Version     string `json:"version"`
		Description string `json:"description"`
	} `json:"info"`
	Tags []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"tags"`
	Paths map[string]map[string]*oaOp `json:"paths"`
}

type oaOp struct {
	Tags        []string              `json:"tags"`
	Summary     string                `json:"summary"`
	Description string                `json:"description"`
	OperationID string                `json:"operationId"`
	Parameters  []oaParam             `json:"parameters"`
	RequestBody *oaBody               `json:"requestBody"`
	Responses   map[string]oaResponse `json:"responses"`
	Curl        string                `json:"x-curl"` // an example request other than the generated one; {base} is the address
}

type oaParam struct {
	Name        string   `json:"name"`
	In          string   `json:"in"`
	Description string   `json:"description"`
	Required    bool     `json:"required"`
	Schema      oaSchema `json:"schema"`
	Example     any      `json:"example"`
}

type oaSchema struct {
	Type    any      `json:"type"`
	Enum    []any    `json:"enum"`
	Default any      `json:"default"`
	Maximum *float64 `json:"maximum"`
	Format  string   `json:"format"`
	Items   *struct {
		Enum []any `json:"enum"`
	} `json:"items"` // a comma-separated list's values
}

type oaBody struct {
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Content     map[string]struct {
		Example any `json:"example"`
	} `json:"content"`
}

type oaResponse struct {
	Description string `json:"description"`
	Content     map[string]struct {
		Schema struct {
			Ref string `json:"$ref"`
		} `json:"schema"`
		Example any `json:"example"`
	} `json:"content"`
}

// loadOpenAPI reads the embedded spec and adds the generated schemas.
func loadOpenAPI() (*openAPI, error) {
	raw := openapiJSON
	o := &openAPI{}
	if err := json.Unmarshal(raw, &o.doc); err != nil {
		return nil, fmt.Errorf("openapi.json: %w", err)
	}
	if err := json.Unmarshal(raw, &o.spec); err != nil {
		return nil, fmt.Errorf("openapi.json: %w", err)
	}
	g := schemaGen{defs: map[string]any{}, types: map[string]reflect.Type{}, order: map[string][]string{}}
	for _, v := range apiSchemaRoots() {
		g.schema(reflect.TypeOf(v))
	}
	if g.err != nil {
		return nil, g.err
	}
	comps, _ := o.doc["components"].(map[string]any)
	if comps == nil {
		comps = map[string]any{}
		o.doc["components"] = comps
	}
	comps["schemas"] = g.defs
	o.order = g.order
	if err := json.Unmarshal(openapiZhJSON, &openapiZh); err != nil {
		return nil, fmt.Errorf("openapi.zh.json: %w", err)
	}
	return o, nil
}

// openapiZh is the API page's Chinese, keyed by operation id, tag and
// parameter name: the spec itself stays English for machines.
type openapiZhSpec struct {
	Info string `json:"info"`
	Tags map[string]struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"tags"`
	Ops map[string]struct {
		Summary     string `json:"summary"`
		Description string `json:"description"`
	} `json:"ops"`
	Params map[string]string `json:"params"`
	Bodies map[string]string `json:"bodies"`
}

var openapiZh openapiZhSpec

// schemas are the generated schemas, by name.
func (o *openAPI) schemas() map[string]any {
	return o.doc["components"].(map[string]any)["schemas"].(map[string]any)
}

// serveSpec serves the spec, its server being the address this request
// came to.
func (o *openAPI) serveSpec(w http.ResponseWriter, r *http.Request) {
	doc := maps.Clone(o.doc)
	doc["servers"] = []map[string]string{{"url": baseURL(r), "description": "This HuntDeck"}}
	writeJSON(w, http.StatusOK, doc)
}

// schemaGen builds JSON Schemas from Go types as encoding/json writes
// them: a named struct becomes a component, a pointer may be null unless
// omitempty leaves it out, and omitempty fields aren't required.
type schemaGen struct {
	defs  map[string]any
	types map[string]reflect.Type
	order map[string][]string
	err   error
}

var timeType = reflect.TypeFor[time.Time]()

func (g *schemaGen) schema(t reflect.Type) map[string]any {
	if t == timeType {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return nullable(g.schema(t.Elem()))
	case reflect.Struct:
		name := schemaName(t)
		if prev, ok := g.types[name]; ok {
			if prev != t && g.err == nil {
				g.err = fmt.Errorf("openapi: %s and %s are both schema %q", prev, t, name)
			}
		} else {
			g.types[name] = t
			g.defs[name] = map[string]any{} // a placeholder while its fields are read
			g.defs[name] = g.object(t, name)
		}
		return map[string]any{"$ref": "#/components/schemas/" + name}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": g.schema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": g.schema(t.Elem())}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	}
	return map[string]any{}
}

// object is a struct's schema: its fields as encoding/json names them,
// embedded structs' fields included.
func (g *schemaGen) object(t reflect.Type, name string) map[string]any {
	props := map[string]any{}
	var required, order []string
	// An outer struct's field takes the place of an embedded one's of the
	// same name, as encoding/json has it (v2's types rely on it): shadowed
	// are the names fields nearer the top have.
	var walk func(t reflect.Type, shadowed map[string]bool)
	walk = func(t reflect.Type, shadowed map[string]bool) {
		own := maps.Clone(shadowed)
		for i := range t.NumField() {
			f := t.Field(i)
			key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			embedded := f.Anonymous && key == "" && f.Type.Kind() == reflect.Struct
			if !embedded && f.IsExported() && key != "-" {
				if key == "" {
					key = f.Name
				}
				own[key] = true
			}
		}
		for i := range t.NumField() {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			key, opts, _ := strings.Cut(tag, ",")
			if f.Anonymous && key == "" && f.Type.Kind() == reflect.Struct {
				walk(f.Type, own)
				continue
			}
			if !f.IsExported() {
				continue
			}
			if key == "" {
				key = f.Name
			}
			if shadowed[key] {
				continue
			}
			omit := strings.Contains(opts, "omitempty")
			ft := f.Type
			if omit && ft.Kind() == reflect.Pointer {
				ft = ft.Elem() // left out rather than null
			}
			props[key] = g.schema(ft)
			order = append(order, key)
			if !omit {
				required = append(required, key)
			}
		}
	}
	walk(t, map[string]bool{})
	g.order[name] = order
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

// nullable lets a schema be null too.
func nullable(s map[string]any) map[string]any {
	if typ, ok := s["type"].(string); ok {
		c := maps.Clone(s)
		c["type"] = []string{typ, "null"}
		return c
	}
	return map[string]any{"anyOf": []any{s, map[string]any{"type": "null"}}}
}

// schemaName names a type's schema: without an "api" prefix or "JSON"
// suffix, capitalised ("apiProgramListJSON" is "ProgramList", "resolvedMonthJSON"
// "Month", store.HuntDetail "HuntDetail").
func schemaName(t reflect.Type) string {
	n := t.Name()
	if rest, ok := strings.CutPrefix(n, "api"); ok && rest != "" && unicode.IsUpper(rune(rest[0])) {
		n = rest
	}
	if rest, ok := strings.CutSuffix(n, "JSON"); ok && rest != "" {
		n = rest
	}
	return strings.ToUpper(n[:1]) + n[1:]
}
