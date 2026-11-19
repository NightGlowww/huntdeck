//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A restore refuses while a server holds the data directory: it would
// otherwise swap the database out from under a running process.
func TestRestoreRefusesWhileServerRuns(t *testing.T) {
	dir := t.TempDir()
	release, err := lockData(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	archive := filepath.Join(dir, "backup.tar.gz")
	if err := os.WriteFile(archive, []byte("not a real archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := restoreCmd([]string{"-from", archive, "-data", dir}, &out, &errOut)
	if code == 0 {
		t.Fatalf("restore ran while the directory was locked:\n%s", out.String())
	}
	if !strings.Contains(errOut.String()+out.String(), "stop it first") {
		t.Errorf("the refusal does not say what to do: %q", errOut.String())
	}
}
