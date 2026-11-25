package config

import "testing"

// TestScanReadBatchStaysSane pins the read-back batch: the scanner's MCP allows
// about ten calls a minute, so the default is eight, a value that cannot be a
// batch falls back to it, and a self-hosted instance can raise it up to a hundred.
func TestScanReadBatchStaysSane(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 8},
		{"0", 8},
		{"-3", 8},
		{"abc", 8},
		{"1", 1},
		{"12", 12},
		{"100", 100},
		{"1000", 100},
	}
	for _, c := range cases {
		if got := scanReadBatch(c.in); got != c.want {
			t.Errorf("scanReadBatch(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
