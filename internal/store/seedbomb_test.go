package store

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A seed that unpacks past the ceiling is refused: a small gzip can expand to
// fill the disk, and refusing late is refusing after the damage.
func TestUnpackRefusesABomb(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seed.db.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	// 4 MiB of zeros against a 1 MiB ceiling: fast, and the same shape.
	zeros := make([]byte, 1<<20)
	for i := 0; i < 4; i++ {
		if _, err := gz.Write(zeros); err != nil {
			t.Fatal(err)
		}
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	var out bytes.Buffer
	err = unpackLimit(path, &out, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("an expansion past the ceiling was accepted: %v", err)
	}
}
