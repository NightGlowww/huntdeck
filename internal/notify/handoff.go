package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mdpsec/huntdeck/internal/scanhandoff"
	"github.com/mdpsec/huntdeck/internal/store"
)

// ReportHandoff queues a channel message about a handoff: what went out, or
// what stopped it. It is best effort - a scanner handoff must not fail because
// a chat webhook is down - so every error is a log line, not a return.
//
// The serve loop and the page call it; the CLI does not, because a command
// that runs in a terminal already prints the same facts to stdout.
func ReportHandoff(ctx context.Context, st *store.Store, log *slog.Logger, sum scanhandoff.Summary, handoffErr error) {
	chans, err := st.NotifyChannels(ctx)
	if err != nil {
		log.WarnContext(ctx, "scan: cannot read channels for the handoff report", "err", err)
		return
	}
	if len(chans) == 0 {
		return
	}
	events := make([]Event, 0, 1)
	switch {
	case handoffErr != nil && sum.Tasks == 0 && sum.Blocked != "":
		events = append(events, Event{Kind: EventScanBlocked, At: st.Now(), Detail: truncate(handoffErr.Error(), 300)})
	case sum.Tasks > 0:
		e := Event{Kind: EventScanHandoff, At: st.Now(),
			Detail: fmt.Sprintf("%d task(s), %d target(s) on node %s: %s", sum.Tasks, sum.Targets, sum.Node, strings.Join(sum.Programs, ", "))}
		events = append(events, e)
	case handoffErr != nil:
		// A failure with nothing created and nothing that blocked on purpose:
		// capacity, a bad config. Worth a line, not a page.
		events = append(events, Event{Kind: EventScanBlocked, At: st.Now(), Detail: truncate(handoffErr.Error(), 300)})
	default:
		return // nothing happened and nothing was asked
	}
	for _, c := range chans {
		rules := ParseRules(c.Rules)
		var matched []Event
		for _, e := range events {
			if m, ok := rules.Match(e); ok {
				matched = append(matched, m)
			}
		}
		if len(matched) == 0 || !c.Instant {
			continue
		}
		outbox, _, err := route([]store.NotifyChannel{c}, matched)
		if err != nil {
			log.WarnContext(ctx, "scan: cannot route the handoff report", "err", err)
			continue
		}
		for _, m := range outbox {
			if err := st.QueueOutbox(ctx, m); err != nil {
				log.WarnContext(ctx, "scan: cannot queue the handoff report", "err", err)
			}
		}
	}
}
