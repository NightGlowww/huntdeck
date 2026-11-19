package config

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every variable the config reads is documented: in the reference, and (but
// for the image's own paths) in .env.example. A new setting can't ship
// without the line that tells a user it exists.
func TestEnvVarsAreDocumented(t *testing.T) {
	src, err := os.ReadFile("config.go")
	if err != nil {
		t.Fatal(err)
	}
	env, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile("../../docs/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	// Variables the image or an alias provides, not the user.
	imageOnly := map[string]bool{"HUNTDECK_NOVNC_DIR": true}

	keys := regexp.MustCompile(`r\.get\("([A-Z0-9_]+)"`).FindAllStringSubmatch(string(src), -1)
	if len(keys) < 30 {
		t.Fatalf("only %d variables found: did the accessor change?", len(keys))
	}
	for _, m := range keys {
		key := m[1]
		if !strings.Contains(string(doc), key) {
			t.Errorf("%s is read but not in docs/configuration.md", key)
		}
		if !imageOnly[key] && !strings.Contains(string(env), key) {
			t.Errorf("%s is read but not in .env.example", key)
		}
	}
}
