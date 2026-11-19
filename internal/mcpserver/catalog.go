package mcpserver

import (
	"sort"
)

// ToolInfo is one tool as a page can show it: the name, what it does, and the
// arguments it takes. It exists so /mcp lists the same registry the server
// serves, rather than a copy that drifts.
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
}

// Catalog is every tool the server offers, in the order it offers them.
func Catalog() []ToolInfo {
	out := make([]ToolInfo, 0, len(tools))
	for _, t := range tools {
		info := ToolInfo{Name: t.Name, Description: t.Description, Write: t.Write}
		for _, name := range sortedKeys(t.Schema) {
			what, typ := describeArg(t.Schema[name])
			info.Arguments = append(info.Arguments, ArgInfo{Name: name, Type: typ, What: what})
		}
		out = append(out, info)
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
