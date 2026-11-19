package scanhandoff

import (
	"context"
	"fmt"

	"github.com/mdpsec/huntdeck/internal/scopesentry"
)

// MinFreePercent is the headroom the scanner's own host must have before
// HuntDeck hands it more work. A scan task is expensive: on a 3.6G box one
// full-template nuclei run took the machine down for hours, so the handoff
// stays out of the way when memory is short.
const MinFreePercent = 25.0

// headroom reports the scanner host's free memory, when it can be read.
// ScopeSentry answers this on /healthz; a failure is not a green light.
var headroom = func(client *scopesentry.Client) (float64, bool, error) {
	return client.MemAvailablePercent(context.Background())
}

// CheckHeadroom refuses a handoff when the scanner host is short on memory.
func CheckHeadroom(ctx context.Context, client *scopesentry.Client) error {
	pct, ok, err := headroom(client)
	if err != nil {
		return fmt.Errorf("cannot read the scanner's memory headroom: %w", err)
	}
	if !ok {
		// Unknown is not permission. The comment said so and the code let it
		// through: a scanner that does not report its memory got a free pass.
		return fmt.Errorf("the scanner did not report its memory headroom: no new scans")
	}
	if pct < MinFreePercent {
		return fmt.Errorf("the scanner host has %.0f%% memory free (needs %.0f%%): no new scans", pct, MinFreePercent)
	}
	return nil
}
