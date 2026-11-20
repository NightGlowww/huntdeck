package web

import (
	"testing"
	"time"
)

// The arithmetic behind "next about 18m": from the last run and the
// interval, with nothing said when either is missing - a wrong next time is
// worse than none.
func TestDueInSaysWhenTheNextPollIs(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		last  time.Time
		inter time.Duration
		want  string
	}{
		{"half the interval gone", now.Add(-15 * time.Minute), 30 * time.Minute, "15m"},
		{"never run", time.Time{}, 30 * time.Minute, ""},
		{"polls off", now.Add(-time.Minute), 0, ""},
		{"already due", now.Add(-time.Hour), 30 * time.Minute, "now"},
	}
	for _, c := range cases {
		if got := dueIn(c.last, c.inter, now); got != c.want {
			t.Errorf("%s: dueIn = %q, want %q", c.name, got, c.want)
		}
	}
}
