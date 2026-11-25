package web

import (
	"strings"
	"testing"
)

// TestOnePerProgramExplainsItself pins the sentence that teaches the interaction:
// the filtered view keeps one row per program, and the row itself opens that
// program's scope tab - which the page never said, so "where are the rest of the
// targets" had no answer a reader could find.
func TestOnePerProgramExplainsItself(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) {})
	_, plain := e.do(e.client(), "GET", "/targets?kind=all", nil, nil)
	_, one := e.do(e.client(), "GET", "/targets?kind=all&one=1", nil, nil)
	want := "one row per program: open a row for the rest of its targets"
	if !strings.Contains(one, want) {
		t.Errorf("the filtered view does not explain itself")
	}
	if strings.Contains(plain, want) {
		t.Errorf("the plain view carries the filter's hint")
	}
}
