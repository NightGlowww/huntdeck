package store

import (
	"testing"
)

// A filter applies before the one-per-program choice: a kind that only a
// program's second-best target has must not hide the program, and the count
// has to agree with the rows.
func TestOnePerProgramRespectsTheKindFilter(t *testing.T) {
	q := newQueueTest(t)
	f := QueueFilter{OnePerProgram: true, Kind: "url"}
	rows, _, err := q.s.HuntQueueTop(q.ctx, f, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Kind != "url" {
			t.Errorf("a %s row came back for a url-only filter", r.Kind)
		}
	}
	n, _, err := q.s.HuntQueueCount(q.ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(rows) {
		t.Errorf("count %d and rows %d disagree under the same filter", n, len(rows))
	}
	// Every program appears once.
	seen := map[int64]bool{}
	for _, r := range rows {
		if seen[r.ProgramID] {
			t.Errorf("program %d appears twice", r.ProgramID)
		}
		seen[r.ProgramID] = true
	}
}
