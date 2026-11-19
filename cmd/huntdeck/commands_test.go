package main

import (
	"os"
	"regexp"
	"testing"
)

// AGENTS.md is the operator's command list. A command it names that the binary
// does not have sends them to "unknown command" — the MCP server had the same
// shape of problem with flags it never read.
//
// Two forms appear there: `/huntdeck <cmd>` inside the shell block, and
// `huntdeck <cmd>` in prose and in the backticked one-liners.
func TestDocumentedCommandsExist(t *testing.T) {
	raw, err := os.ReadFile("../../AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	forms := []*regexp.Regexp{
		// Not `bin/huntdeck and …`: the slash must start the word.
		regexp.MustCompile(`(?:^|\s)/huntdeck\s+([a-z][a-z-]+)`),
		regexp.MustCompile("`huntdeck\\s+([a-z][a-z-]+)"),
	}
	seen := map[string]bool{}
	for _, re := range forms {
		for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
			seen[m[1]] = true
		}
	}
	// A positive control: these four are certainly in the file, so finding them
	// proves the guard is reading it rather than matching nothing.
	for _, must := range []string{"status", "queue", "doctor", "backup"} {
		if !seen[must] {
			t.Fatalf("the guard did not find %q in AGENTS.md: it is not reading the file", must)
		}
	}
	// Verbs that belong to docker rather than to the binary.
	skip := map[string]bool{"version": true, "help": true}
	for name := range seen {
		if skip[name] {
			continue
		}
		if _, ok := commands[name]; ok {
			continue
		}
		t.Errorf("AGENTS.md documents \"huntdeck %s\", which the binary does not have", name)
	}
}
