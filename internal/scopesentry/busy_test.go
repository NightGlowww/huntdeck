package scopesentry

import (
	"errors"
	"testing"
)

// The scanner has two ways of saying "not now", and both were verbatim in the
// instance's answers: a full node queue and a per-minute creation limit. Telling
// them apart from breakage is what lets the page say "wait" instead of "failed".
func TestIsBusyRecognisesTheScannersOwnRefusals(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"node queue full",
			errors.New("scopesentry tools/call: 节点 node-sh-01 队列已积压 5 个任务（上限 5），请等待当前任务完成后再提交"), true},
		{"creation limit", errors.New("scopesentry: api.task.rate_limited 每分钟最多创建 10 个任务"), true},
		{"wrapped", errors.Join(ErrBusy, errors.New("x")), true},
		{"a real failure", errors.New("scopesentry tools/call: HTTP 403"), false},
		{"auth", errors.New("API key is disabled"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsBusy(c.err); got != c.want {
				t.Fatalf("IsBusy(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// busy() keeps the scanner's own words: an operator reading "wait" needs to know
// which limit they hit.
func TestBusyKeepsTheScannersWords(t *testing.T) {
	in := errors.New("节点 node-sh-01 队列已积压 5 个任务（上限 5）")
	out := busy(in)
	if !errors.Is(out, ErrBusy) {
		t.Fatalf("busy() did not wrap ErrBusy: %v", out)
	}
	if !IsBusy(out) || out.Error() == "" {
		t.Fatalf("busy() lost the message: %v", out)
	}
	if busy(errors.New("HTTP 500")) != nil && errors.Is(busy(errors.New("HTTP 500")), ErrBusy) {
		t.Fatal("a real failure was labelled busy")
	}
}
