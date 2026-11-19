package mcpserver

import (
	"sort"
)

// ToolInfo is one tool as a page can show it: the name, what it does, and the
// arguments it takes. It exists so /mcp lists the same registry the server
// answers tools/list from, rather than a copy that drifts.
type ToolInfo struct {
	Name        string
	Description string
	Arguments   []ArgInfo
	Write       bool
}

// ArgInfo is one argument of a tool, with its type and what it is for.
type ArgInfo struct {
	Name string
	Type string
	What string
	// Required marks an argument the tool will not run without.
	Required bool
}

// Catalog is every tool the server offers, in the order it offers them.
func Catalog() []ToolInfo {
	out := make([]ToolInfo, 0, len(tools))
	for _, t := range tools {
		info := ToolInfo{Name: t.Name, Description: t.Description, Write: t.Write}
		// The schema is a JSON Schema object: the arguments are under
		// "properties", not its own keywords (type, additionalProperties).
		props, _ := t.Schema["properties"].(map[string]any)
		needed := requiredSet(t.Schema)
		for _, name := range sortedKeys(props) {
			what, typ := describeArg(props[name])
			info.Arguments = append(info.Arguments, ArgInfo{
				Name: name, Type: typ, What: what, Required: needed[name],
			})
		}
		out = append(out, info)
	}
	return out
}

// requiredSet reads the schema's required list. The helper that builds the
// schemas stores []string; a hand-written one may carry []any.
func requiredSet(schema map[string]any) map[string]bool {
	out := map[string]bool{}
	switch list := schema["required"].(type) {
	case []string:
		for _, name := range list {
			out[name] = true
		}
	case []any:
		for _, v := range list {
			if name, ok := v.(string); ok {
				out[name] = true
			}
		}
	}
	return out
}

func sortedKeys(schema map[string]any) []string {
	keys := make([]string, 0, len(schema))
	for k := range schema {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// describeArg pulls the type and the sentence out of one property schema.
func describeArg(v any) (what, typ string) {
	m, ok := v.(map[string]any)
	if !ok {
		return "", ""
	}
	if s, ok := m["description"].(string); ok {
		what = s
	}
	if s, ok := m["type"].(string); ok {
		typ = s
	}
	return what, typ
}
