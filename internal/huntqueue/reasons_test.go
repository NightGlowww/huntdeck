package huntqueue

import (
	"testing"
)

// TestHandoffReasonSaysWhatTheScannerSaid pins the queue's first reason: a
// handed-over target used to say only "already handed to the scanner", which
// reads the same whether the scan finished, is running, or failed. The reader
// decides what to hunt from this line, so it carries the scanner's answer.
func TestHandoffReasonSaysWhatTheScannerSaid(t *testing.T) {
	cases := []struct {
		state string
		want  string
	}{
		{"done", "already scanned: the scanner finished it"},
		{"failed", "the scanner failed on it: hand it over again"},
		{"gone", "the scanner no longer has it: hand it over again"},
		{"running", "the scanner is on it now"},
		{"queued", "the scanner is on it now"},
		{"", "already handed to the scanner"},
	}
	for _, c := range cases {
		got := reasons(Inputs{HandedOff: true, ScanState: c.state})
		if len(got) == 0 {
			t.Fatalf("%q: no reasons", c.state)
		}
		if got[0].Key != c.want {
			t.Errorf("state %q: first reason = %q, want %q", c.state, got[0].Key, c.want)
		}
	}
	// Not handed over: the reason must not appear at all.
	for _, r := range reasons(Inputs{ScanState: "done"}) {
		if r.Key == "already scanned: the scanner finished it" {
			t.Error("a target that was never handed over says it was scanned")
		}
	}
}
