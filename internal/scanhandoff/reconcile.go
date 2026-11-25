package scanhandoff

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mdpsec/huntdeck/internal/scopesentry"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Reconcile reads what the scanner says about the tasks HuntDeck created and
// records it, which is the last open link in docs/pipeline.md: until now a
// finished or failed task was known to the scanner alone.
//
// It reads the newest tasks first and stops at a task the scanner does not
// know (deleted there): a task that is gone is not an error, it just stops the
// walk, because the ones after it are older still.
//
// A task already read as done or failed is not asked about again: the answer
// cannot change, and the scanner's API costs a request.
type ReconcileSummary struct {
	Tasks  int
	Read   int
	Done   int
	Failed int
	Gone   int
}

func Reconcile(ctx context.Context, st *store.Store, client *scopesentry.Client, limit int, now time.Time) (ReconcileSummary, error) {
	var sum ReconcileSummary
	tasks, err := st.HandoffTasks(ctx, limit)
	if err != nil {
		return sum, err
	}
	sum.Tasks = len(tasks)
	for _, t := range tasks {
		if t.State == "done" || t.State == "failed" {
			continue
		}
		state, err := client.State(ctx, t.TaskID)
		if errors.Is(err, scopesentry.ErrUnknownTask) {
			// Deleted on the scanner's side: record it and carry on, the older
			// tasks after it are still worth reading.
			if err := st.SetHandoffState(ctx, t.TaskID, "gone", -1, now); err != nil {
				return sum, fmt.Errorf("reconcile %s: %w", t.TaskID, err)
			}
			sum.Gone++
			continue
		}
		if err != nil {
			// A network or auth failure: the pass stops rather than recording
			// every task as gone.
			return sum, fmt.Errorf("reconcile %s: %w", t.TaskID, err)
		}
		reading := scopesentry.StateReading(state.Status)
		if err := st.SetHandoffState(ctx, t.TaskID, reading, state.Status, now); err != nil {
			return sum, fmt.Errorf("reconcile %s: %w", t.TaskID, err)
		}
		sum.Read++
		switch reading {
		case "done":
			sum.Done++
		case "failed":
			sum.Failed++
		}
	}
	return sum, nil
}
