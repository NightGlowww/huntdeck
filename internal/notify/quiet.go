package notify

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// queueQuiet tells the user which of their reports have gone quiet: still
// undecided after the days Settings › Reports sets, with no word from the
// program. It is derived rather than a change the platforms report, so nothing
// else would say it - a report could sit for a month with only the page's mark
// showing it.
//
// Routed like every other event, so a channel that chooses this kind gets it
// and one that does not stays quiet. Each silence is announced once: the
// report is stamped, and a new word from the program makes it eligible again.
//
// Best effort: a store error must not stop the tick's other work.
func (n *Notifier) queueQuiet(ctx context.Context, now time.Time) error {
	days, err := n.Store.ReportQuietDays(ctx, quietDefaultDays)
	if err != nil || days < 1 {
		return err
	}
	reports, err := n.Store.QuietReports(ctx, days, now)
	if err != nil {
		return fmt.Errorf("quiet reports: %w", err)
	}
	if len(reports) == 0 {
		return nil
	}
	chans, err := n.channels(ctx)
	if err != nil {
		return err
	}
	events := make([]Event, 0, len(reports))
	ids := make([]int64, 0, len(reports))
	for _, r := range reports {
		events = append(events, Event{
			Kind: EventReportQuiet, At: now, Platform: r.Platform,
			Detail: "no word for " + strconv.Itoa(r.QuietDays) + " days",
			Report: &Report{ID: r.ID, Title: r.Title, URL: r.URL, State: r.State, Severity: r.Severity,
				ProgramName: r.ProgramName, ProgramHandle: r.ProgramHandle},
		})
		ids = append(ids, r.ID)
	}
	outbox, pending, err := route(chans, events)
	if err != nil {
		return err
	}
	if err := n.Store.QueueOutboxAndPending(ctx, outbox, pending); err != nil {
		return fmt.Errorf("quiet reports: queue: %w", err)
	}
	if err := n.Store.MarkQuietNotified(ctx, ids, now); err != nil {
		return fmt.Errorf("quiet reports: stamp: %w", err)
	}
	return nil
}

// quietDefaultDays mirrors reports.DefaultQuietDays without the import cycle
// a display package would bring.
const quietDefaultDays = 14
