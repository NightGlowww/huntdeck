package notify

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/scanhandoff"
	"github.com/mdpsec/huntdeck/internal/store"
)

func handoffEnv(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Migrate(ctx, migs, t.TempDir(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	return store.New(d, nil)
}

// outboxFor reads a channel's queued-but-unsent messages through the store's
// own query path (DueOutbox is what the sender uses).
func outboxFor(t *testing.T, st *store.Store, channelID int64) []Event {
	t.Helper()
	items, err := st.DueOutbox(context.Background(), st.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var out []Event
	for _, it := range items {
		if it.ChannelID != channelID {
			continue
		}
		var evs []Event
		if err := json.Unmarshal([]byte(it.Events), &evs); err != nil {
			t.Fatalf("bad events JSON: %v", err)
		}
		out = append(out, evs...)
	}
	return out
}

// A handoff that created tasks queues one message for a channel whose rules
// include scan_handoff, and nothing for one whose rules leave it out.
func TestReportHandoffQueuesForMatchingChannels(t *testing.T) {
	ctx := context.Background()
	st := handoffEnv(t)
	log := slog.New(slog.DiscardHandler)

	on, err := st.SaveNotifyChannel(ctx, store.NotifyChannel{Kind: store.NotifyWebhook, Name: "on", Enabled: true, Instant: true, Rules: Rules{Events: []string{EventScanHandoff}}.JSON()})
	if err != nil {
		t.Fatal(err)
	}
	off, err := st.SaveNotifyChannel(ctx, store.NotifyChannel{Kind: store.NotifyWebhook, Name: "off", Enabled: true, Instant: true, Rules: Rules{Events: []string{EventScopeAdded}}.JSON()})
	if err != nil {
		t.Fatal(err)
	}

	sum := scanhandoff.Summary{Tasks: 2, Targets: 3, Node: "node-1", Programs: []string{"Acme"}}
	ReportHandoff(ctx, st, log, sum, nil)

	events := outboxFor(t, st, on)
	if len(events) != 1 || events[0].Kind != EventScanHandoff {
		t.Fatalf("matching channel events: %+v", events)
	}
	if other := outboxFor(t, st, off); len(other) != 0 {
		t.Fatalf("the non-matching channel got %d events", len(other))
	}
}

// A blocked batch reports itself, so the operator hears why nothing was
// created instead of finding an empty queue later.
func TestReportHandoffReportsABlock(t *testing.T) {
	ctx := context.Background()
	st := handoffEnv(t)
	log := slog.New(slog.DiscardHandler)

	ch, err := st.SaveNotifyChannel(ctx, store.NotifyChannel{Kind: store.NotifyWebhook, Name: "on", Enabled: true, Instant: true, Rules: Rules{Events: []string{EventScanHandoff, EventScanBlocked}}.JSON()})
	if err != nil {
		t.Fatal(err)
	}

	ReportHandoff(ctx, st, log, scanhandoff.Summary{}, errBlocked{})

	events := outboxFor(t, st, ch)
	if len(events) != 1 || events[0].Kind != EventScanBlocked {
		t.Fatalf("a block produced %+v, want one scan_blocked", events)
	}
}

// errBlocked stands in for a runOpinion refusal.
type errBlocked struct{}

func (e errBlocked) Error() string {
	return "the decision model says block (confidence 0.93): no scan tasks created"
}

// A channel with a digest gets the event as a pending row, so the daily
// summary includes handoffs like everything else.
func TestReportHandoffDigest(t *testing.T) {
	ctx := context.Background()
	st := handoffEnv(t)
	log := slog.New(slog.DiscardHandler)

	ch, err := st.SaveNotifyChannel(ctx, store.NotifyChannel{Kind: store.NotifyWebhook, Name: "dig", Enabled: true, DigestAt: "09:00", Rules: Rules{Events: []string{EventScanHandoff}}.JSON()})
	if err != nil {
		t.Fatal(err)
	}

	ReportHandoff(ctx, st, log, scanhandoff.Summary{Tasks: 1, Targets: 2, Node: "n", Programs: []string{"Acme"}}, nil)

	pending, err := st.PendingEvents(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("a digest channel has %d pending events, want 1", len(pending))
	}
	var ev Event
	if err := json.Unmarshal([]byte(pending[0].Event), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Kind != EventScanHandoff {
		t.Fatalf("pending event kind %q", ev.Kind)
	}
}
