package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// The API page (/api): the OpenAPI description (openapi.go) as a reference,
// an endpoint a panel, with its parameters, an example request and a
// sample response; a GET can be sent from the page (api.js) to see what
// this HuntDeck answers.

type apiDocsData struct {
	Base        string // how this browser reached HuntDeck
	Version     string // the API's
	Description string
	Groups      []apiDocGroup
	ReadOnly    bool // a read-only copy: no write endpoints, and a note on scripted use
	// Password says this instance asks for its access password, so the login
	// paragraph must not claim scripts need no credential.
	Password bool
	// Tokens are the API tokens, live and retired; NewToken is the one just
	// minted, shown once; Flash says what just happened.
	Tokens   []apiTokenRow
	NewToken string
	Flash    string
}

type apiDocGroup struct {
	Name, ID, Description string
	Ops                   []apiDocOp
}

type apiDocOp struct {
	ID, Method, Path, Summary, Description string
	Params                                 []apiDocParam
	Body, BodyType                         string // a request body, and its content type
	Try                                    string // the example request's path and query
	Curl                                   string
	Status                                 string // the success status, "200"
	Sample                                 string // a response shaped as its schema
	Live                                   bool   // a GET: it can be sent from the page
}

type apiDocParam struct {
	Name, In, Type, Default, Values, Description string
	Required                                     bool
}

// sampleLines bounds a sample response; Send shows the whole answer.
const sampleLines = 34

// privateNote is the spec description's word on where HuntDeck is
// reached, which a public read-only copy leaves out (TestReadOnlyAPIPage
// checks it's still there to replace).
const privateNote = "There is no login and no token: HuntDeck is reached only from your own machine or private network."

// servedReadOnly says whether a read-only copy serves an API route: GETs
// it doesn't hide, and POSTs that only read.
func servedReadOnly(method, path string) bool {
	for _, p := range readOnlyHidden {
		if path == p || strings.HasPrefix(path, p+"/") {
			return false
		}
	}
	return method == http.MethodGet || readOnlyPOST[path]
}

// apiDocs is the page an ordinary GET renders.
func (s *server) apiDocs(w http.ResponseWriter, r *http.Request) {
	s.renderAPIDocs(w, r, "", "")
}

// renderAPIDocs builds the page; the token handlers reuse it to show a freshly
// minted token once, in the body, and a short confirmation otherwise.
func (s *server) renderAPIDocs(w http.ResponseWriter, r *http.Request, newToken, flash string) {
	o := s.openapi
	// The page's own words (summaries, descriptions, tags) come from the
	// spec, in English; openapi.zh.json has the Chinese by operation id, tag
	// and parameter, and the spec itself stays English for machines.
	zh := s.langOf(r) == "zh"
	description := o.spec.Info.Description
	if zh && openapiZh.Info != "" {
		description = openapiZh.Info
	}
	d := apiDocsData{Base: baseURL(r), Version: o.spec.Info.Version, Description: description, Password: s.auth != nil}
	// Path examples use a real program, so a GET sent from the page finds one.
	platform, handle := "hackerone", "acme"
	if progs, _, err := s.Store.Programs(r.Context(), store.ProgramFilter{State: "open", Sort: "huntscore", Limit: 1}); err == nil && len(progs) > 0 {
		platform, handle = progs[0].Platform, progs[0].Handle
	}
	groups := map[string]*apiDocGroup{}
	for _, t := range o.spec.Tags {
		group := apiDocGroup{Name: t.Name, ID: slug(t.Name), Description: t.Description}
		if zh {
			if tr, ok := openapiZh.Tags[t.Name]; ok {
				group.Name, group.Description = tr.Name, tr.Description
			}
		}
		d.Groups = append(d.Groups, group)
	}
	for i := range d.Groups { // keyed by the spec's own tag name, not the shown one
		groups[o.spec.Tags[i].Name] = &d.Groups[i]
	}
	d.ReadOnly = s.ReadOnly
	if s.ReadOnly { // not "reached only from your own machine": this one is public
		d.Description = strings.Replace(d.Description, privateNote, "There is no login and no token.", 1)
	} else if d.Password {
		// The spec speaks for a default install; this one has a password, and
		// saying otherwise was a lie the page kept telling.
		d.Description = strings.Replace(d.Description, privateNote,
			"This instance asks for its access password; a browser session or an API token (below) is the credential.", 1)
	}
	for _, rt := range s.apiRoutes() {
		method, path, _ := strings.Cut(rt.Pattern, " ")
		if s.ReadOnly && !servedReadOnly(method, path) {
			continue // a read-only copy refuses it: not worth listing
		}
		op := o.spec.Paths[path][strings.ToLower(method)]
		if op == nil || len(op.Tags) == 0 || groups[op.Tags[0]] == nil {
			continue // a test makes sure there are none
		}
		groups[op.Tags[0]].Ops = append(groups[op.Tags[0]].Ops, o.docOp(method, path, op, d.Base, platform, handle, zh))
	}
	d.NewToken, d.Flash = newToken, flash
	if tokens, err := s.Store.APITokens(r.Context()); err == nil {
		d.Tokens = tokenRows(tokens)
	}
	s.render(w, r, http.StatusOK, "api", pageData{Title: "API", Nav: "api", Data: d})
}

// docOp lays out one operation, its examples using base and, for path
// parameters, platform and handle.
func (o *openAPI) docOp(method, path string, op *oaOp, base, platform, handle string, zh bool) apiDocOp {
	v := apiDocOp{ID: op.OperationID, Method: method, Path: path, Summary: op.Summary, Description: op.Description, Live: method == http.MethodGet}
	if zh {
		if tr, ok := openapiZh.Ops[op.OperationID]; ok {
			if tr.Summary != "" {
				v.Summary = tr.Summary
			}
			if tr.Description != "" {
				v.Description = tr.Description
			}
		}
	}
	fill := strings.NewReplacer("{platform}", platform, "{handle}", url.PathEscape(handle))
	query := url.Values{}
	var keys []string
	for _, p := range op.Parameters {
		paramDesc := p.Description
		if zh {
			if tr, ok := openapiZh.Params[p.Name]; ok {
				paramDesc = tr
			}
		}
		dp := apiDocParam{Name: p.Name, In: p.In, Type: fmt.Sprint(p.Schema.Type), Description: paramDesc, Required: p.Required}
		if p.Schema.Default != nil {
			dp.Default = fmt.Sprint(p.Schema.Default)
		}
		var vals []string
		enum := p.Schema.Enum
		if p.Schema.Items != nil {
			enum = p.Schema.Items.Enum
			dp.Type = "list, comma-separated"
		}
		for _, e := range enum {
			vals = append(vals, fmt.Sprint(e))
		}
		dp.Values = strings.Join(vals, " · ")
		v.Params = append(v.Params, dp)
		if p.In == "query" && p.Example != nil {
			query.Set(p.Name, fmt.Sprint(p.Example))
			keys = append(keys, p.Name)
		}
	}
	v.Try = fill.Replace(path)
	if len(keys) > 0 {
		parts := make([]string, len(keys)) // in the spec's order, not sorted
		for i, k := range keys {
			parts[i] = url.QueryEscape(k) + "=" + url.QueryEscape(query.Get(k))
		}
		v.Try += "?" + strings.Join(parts, "&")
	}
	switch {
	case op.Curl != "":
		v.Curl = fill.Replace(strings.ReplaceAll(op.Curl, "{base}", base))
	case method == http.MethodGet:
		v.Curl = `curl -s "` + base + v.Try + `"`
	default:
		v.Curl = `curl -s -X ` + method + ` "` + base + v.Try + `"`
	}
	if b := op.RequestBody; b != nil {
		v.Body = b.Description
		if zh {
			if tr, ok := openapiZh.Bodies[op.OperationID]; ok {
				v.Body = tr
			}
		}
		for ct := range b.Content {
			v.BodyType = ct
		}
	}
	for _, code := range []string{"200", "202"} {
		res, ok := op.Responses[code]
		if !ok {
			continue
		}
		v.Status = code
		for _, c := range res.Content {
			if name, ok := strings.CutPrefix(c.Schema.Ref, "#/components/schemas/"); ok {
				v.Sample = o.sample(name)
			}
		}
		break
	}
	return v
}

// sample is a response shaped as schema name says, a list's first item
// standing for the rest, cut at sampleLines.
func (o *openAPI) sample(name string) string {
	b, err := json.MarshalIndent(o.sampleOf(map[string]any{"$ref": "#/components/schemas/" + name}, "", 0), "", "  ")
	if err != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) > sampleLines {
		lines = append(lines[:sampleLines], "  …")
	}
	return strings.Join(lines, "\n")
}

// orderedObject marshals its keys in order.
type orderedObject struct {
	keys []string
	vals map[string]any
}

func (m orderedObject) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range m.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, err := json.Marshal(m.vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// sampleStrings are sample values for string fields, by name.
var sampleStrings = map[string]string{
	"platform": "hackerone", "handle": "acme", "name": "Acme", "url": "https://hackerone.com/acme", "state": "open",
	"kind": "wildcard", "via_kind": "wildcard", "value": "*.example.com", "via": "*.example.com", "identifier": "*.example.com",
	"host": "app.example.com", "input": "app.example.com", "currency": "USD", "verdict": "in", "status": "ok", "mode": "quick",
	"asset_type": "wildcard", "severity": "high", "title": "Stored XSS in profile names", "month": "2026-10", "confidence": "high",
	"key": "fresh", "preset": "balanced", "version": "1.0.0", "sort": "paid90", "description": "Added *.example.com to scope",
	"error": "a sentence saying what went wrong",
}

func (o *openAPI) sampleOf(s map[string]any, field string, depth int) any {
	if depth > 8 {
		return nil
	}
	if ref, ok := s["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		def, _ := o.schemas()[name].(map[string]any)
		props, _ := def["properties"].(map[string]any)
		obj := orderedObject{vals: map[string]any{}}
		for _, k := range o.order[name] {
			ps, _ := props[k].(map[string]any)
			obj.keys = append(obj.keys, k)
			obj.vals[k] = o.sampleOf(ps, k, depth+1)
		}
		return obj
	}
	if alts, ok := s["anyOf"].([]any); ok && len(alts) > 0 {
		first, _ := alts[0].(map[string]any)
		return o.sampleOf(first, field, depth)
	}
	typ := s["type"]
	if list, ok := typ.([]string); ok && len(list) > 0 {
		typ = list[0]
	}
	switch typ {
	case "object":
		if add, ok := s["additionalProperties"].(map[string]any); ok {
			return map[string]any{"key": o.sampleOf(add, "", depth+1)}
		}
		return map[string]any{}
	case "array":
		items, _ := s["items"].(map[string]any)
		return []any{o.sampleOf(items, strings.TrimSuffix(field, "s"), depth+1)}
	case "string":
		if s["format"] == "date-time" {
			return "2026-11-05T12:00:00Z"
		}
		if v, ok := sampleStrings[field]; ok {
			return v
		}
		return "…"
	case "integer", "number":
		return 0
	case "boolean":
		return false
	}
	return nil
}

// slug makes an anchor of a name: "Scope check" is "scope-check".
func slug(name string) string {
	return strings.ReplaceAll(strings.ToLower(name), " ", "-")
}

// apiTokensPanel lists the tokens and the form to mint one.
type apiTokenRow struct {
	ID       int64
	Name     string
	Created  time.Time
	LastUsed *time.Time
	Revoked  *time.Time
}

func tokenRows(list []store.APIToken) []apiTokenRow {
	out := make([]apiTokenRow, 0, len(list))
	for _, t := range list {
		if t.Kind == "mcp" {
			// The MCP page manages its own keys; they do not belong here.
			continue
		}

		out = append(out, apiTokenRow{ID: t.ID, Name: t.Name, Created: t.CreatedAt, LastUsed: t.LastUsedAt, Revoked: t.RevokedAt})
	}
	return out
}

// newToken makes a token from 32 random bytes, hex-encoded: only its digest is
// stored, and it is shown once on the page that created it.
func newTokenValue() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// createAPIToken mints one and shows it once, in the response body rather than a
// redirect, so it never reaches a URL bar, a history or a log.
func (s *server) createAPIToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" {
		name = "token"
	}
	if len(name) > 60 {
		name = name[:60]
	}
	value, err := newTokenValue()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if _, err := s.Store.CreateAPIToken(r.Context(), "api", name, value); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderAPIDocs(w, r, value, "token-created")
}

// rotateAPIToken replaces a token's secret in place: same row, same name,
// same created time. The old value stops working the moment the digest is
// swapped, and the new one is shown once, like a creation.
func (s *server) rotateAPIToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	id, err := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	if err != nil {
		s.renderAPIDocs(w, r, "", "token-missing")
		return
	}
	value, err := newTokenValue()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.Store.ReplaceAPITokenDigest(r.Context(), id, value); err != nil {
		s.renderAPIDocs(w, r, "", "token-missing")
		return
	}
	s.renderAPIDocs(w, r, value, "token-rotated")
}

// deleteAPIToken drops a revoked row. A live token is refused: revoke first,
// so one click cannot remove something a script is still sending.
func (s *server) deleteAPIToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	id, err := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	if err != nil {
		s.renderAPIDocs(w, r, "", "token-missing")
		return
	}
	if err := s.Store.DeleteAPIToken(r.Context(), id); err != nil {
		s.renderAPIDocs(w, r, "", "token-not-revoked")
		return
	}
	s.renderAPIDocs(w, r, "", "token-deleted")
}

// revokeAPIToken retires one.
func (s *server) revokeAPIToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	id, err := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	if err != nil {
		s.renderAPIDocs(w, r, "", "token-revoked")
		return
	}
	if err := s.Store.RevokeAPIToken(r.Context(), id); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderAPIDocs(w, r, "", "token-revoked")
}
