package main

import (
	"bytes"
	"context"
	"testing"
)

// openApp wires the whole server together and nothing tested it: a sweep placed
// three lines before the store existed dereferenced nil and the container
// crash-looped at startup, with every unit test green. This walks the same path
// the serve command walks.
func TestOpenAppWiresEveryPart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HUNTDECK_DATA_DIR", dir)
	t.Setenv("HUNTDECK_SEED_URL", "off")
	t.Setenv("HUNTDECK_PASSWORD", "")

	var buf bytes.Buffer
	a, ok := openApp(context.Background(), &buf, true)
	if !ok {
		t.Fatalf("openApp failed: %s", buf.String())
	}
	defer a.close()
	if a.db == nil || a.store == nil || a.syncer == nil {
		t.Fatalf("a part is nil: db=%v store=%v syncer=%v", a.db != nil, a.store != nil, a.syncer != nil)
	}
	// The sweep ran here, so a fresh database has nothing left running.
	if running, err := a.store.RunningRuns(context.Background()); err != nil || len(running) != 0 {
		t.Fatalf("fresh database has runs: %v %v", running, err)
	}
}
