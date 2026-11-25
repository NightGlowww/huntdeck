package notify

import (
	"context"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// TestQuietNeedsAChannelAndAnnouncesOnce pins two things at once: a silence is
// not consumed when nothing can be delivered (a stamp without a channel would
// swallow the notice for a channel set up tomorrow), and once a channel does
// take it, the same silence is not announced again.
func TestQuietNeedsAChannelAndAnnouncesOnce(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	now := e.clock
	old, activity := now.Add(-40*24*time.Hour), now.Add(-30*24*time.Hour)
	if _, err := e.store.SaveReports(ctx, "hackerone", []platform.Report{{
		Key: "q-1", Title: "Silent", State: platform.ReportNew,
		SubmittedAt: old, LastActivityAt: &activity,
	}}); err != nil {
		t.Fatal(err)
	}

	// No channel: nothing queued, and the silence stays eligible.
	if err := e.n.queueQuiet(ctx, now); err != nil {
		t.Fatal(err)
	}
	if quiet, _ := e.store.QuietReports(ctx, 14, now); len(quiet) != 1 {
		t.Fatal("the silence was consumed with no channel to deliver it")
	}

	e.channel("hook", store.NotifyWebhook, Config{URL: "https://example.com/hook"}, DefaultRules(), true, "")

	if err := e.n.queueQuiet(ctx, now); err != nil {
		t.Fatal(err)
	}
	if quiet, _ := e.store.QuietReports(ctx, 14, now); len(quiet) != 0 {
		t.Fatal("still eligible after the notice went out")
	}

	// Nothing left to announce: a second pass queues nothing.
	if err := e.n.queueQuiet(ctx, now); err != nil {
		t.Fatal(err)
	}
	if due, err := e.store.DueOutbox(ctx, now); err != nil {
		t.Fatal(err)
	} else if len(due) != 1 {
		t.Fatalf("queued %d messages, want 1", len(due))
	}
}
