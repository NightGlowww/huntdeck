package i18n

import (
	"embed"
	"encoding/json"
)

// The scripts' words (static/*.js) are a small separate catalog: a page
// carries only what its scripts need, not the whole interface.

//go:embed catalog/js/*.json
var jsCatalogFS embed.FS

// JS returns lang's messages for the scripts, or nil when it has none.
func JS(lang string) map[string]string {
	b, err := jsCatalogFS.ReadFile("catalog/js/" + Normalize(lang) + ".json")
	if err != nil {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}
