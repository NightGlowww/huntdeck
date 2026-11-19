package scanhandoff

import (
	"context"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/scopesentry"
)

// The guard is the lesson of the host that went down: a full-template scan on
// the machine that also serves the app took it out for hours. It must refuse
// when the scanner's host is short, and it must not treat "cannot read" as a
// green light.
func TestHeadroomRefusesWhenShort(t *testing.T) {
	cases := []struct {
		name    string
		pct     float64
		known   bool
		err     error
		wantErr bool
	}{
		{"plenty", 68, true, nil, false},
		{"exactly the floor", MinFreePercent, true, nil, false},
		{"just under", MinFreePercent - 0.1, true, nil, true},
		{"nearly out", 4, true, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			saved := headroom
			defer func() { headroom = saved }()
			headroom = func(*scopesentry.Client) (float64, bool, error) {
				return c.pct, c.known, c.err
			}
			err := CheckHeadroom(context.Background(), &scopesentry.Client{})
			if c.wantErr && err == nil {
				t.Fatalf("%.1f%% free was allowed", c.pct)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("%.1f%% free was refused: %v", c.pct, err)
			}
			if err != nil && !strings.Contains(err.Error(), "memory") {
				t.Errorf("the refusal should say why: %v", err)
			}
		})
	}
}
