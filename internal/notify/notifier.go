package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// BaseURLSetting is the settings key holding HuntDeck's address, for links
// in messages ("" for none: links go to the platform).
const BaseURLSetting = "notify.base_url"

// Delivery limits.
const (
	tick          = 15 * time.Second
	maxAttempts   = 8
	failureStreak = 3 // failed polls in a row before a platform alert
	keepOutbox    = 30 * 24 * time.Hour
)

// Notifier turns polls into notifications and delivers them.
type Notifier struct {
	Store *store.Store
	Log   *slog.Logger
	// Client sends messages; it must have a timeout.
	Client *http.Client
	// TelegramAPI is the Bot API's base URL (tests point it elsewhere).
	TelegramAPI string
	// Timezone is the default zone for digest times (see store.Timezone).
	Timezone string

	pruned time.Time
}

// New returns a notifier with a 20-second HTTP timeout.
func New(st *store.Store, log *slog.Logger, timezone string) *Notifier {
	return &Notifier{Store: st, Log: log, Client: &http.Client{Timeout: 20 * time.Second},
		TelegramAPI: "https://api.telegram.org", Timezone: timezone}
}

// Run works every few seconds until ctx ends.
func (n *Notifier) Run(ctx context.Context) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		if err := n.Tick(ctx); err != nil && ctx.Err() == nil {
			n.Log.Warn("notifications", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick queues notifications for finished polls, pauses that lasted and due
// digests, then sends
// what's due.
func (n *Notifier) Tick(ctx context.Context) error {
	now := n.Store.Now()
	err := errors.Join(n.queueRuns(ctx), n.queueLongPauses(ctx, now), n.queueReportNotices(ctx, now), n.queueDigests(ctx, now),
		n.sendDue(ctx, now))
	if now.Sub(n.pruned) > time.Hour {
		n.pruned = now
		err = errors.Join(err, n.Store.PruneOutbox(ctx, now.Add(-keepOutbox)))
	}
	return err
}

func (n *Notifier) channels(ctx context.Context) ([]store.NotifyChannel, error) {
	all, err := n.Store.NotifyChannels(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.NotifyChannel
	for _, c := range all {
		if c.Enabled {
			out = append(out, c)
		}
	}
	return out, nil
}

// queueRuns turns each finished poll's changes into events and queues them
// for the channels whose rules match.
func (n *Notifier) queueRuns(ctx context.Context) error {
	runs, err := n.Store.UnnotifiedRuns(ctx)
	if err != nil || len(runs) == 0 {
		return err
	}
	chans, err := n.channels(ctx)
	if err != nil {
		return err
	}
	for _, run := range runs {
		events, err := n.runEvents(ctx, run)
		if err != nil {
			return fmt.Errorf("poll %d: %w", run.ID, err)
		}
		outbox, pending, err := route(chans, events)
		if err != nil {
			return err
		}
		if err := n.Store.QueueRun(ctx, run.ID, outbox, pending); err != nil {
			return fmt.Errorf("poll %d: %w", run.ID, err)
		}
	}
	return nil
}

// route matches events to each channel's rules: a message for a channel
// that sends at once, an event for each match for one with a digest.
func route(chans []store.NotifyChannel, events []Event) ([]store.OutboxMessage, []store.PendingEvent, error) {
	var outbox []store.OutboxMessage
	var pending []store.PendingEvent
	for _, c := range chans {
		rules := ParseRules(c.Rules)
		var matched []Event
		for _, e := range events {
			if m, ok := rules.Match(e); ok {
				matched = append(matched, m)
			}
		}
		if len(matched) == 0 {
			continue
		}
		if c.Instant {
			b, err := json.Marshal(matched)
			if err != nil {
				return nil, nil, err
			}
			outbox = append(outbox, store.OutboxMessage{ChannelID: c.ID, Kind: store.OutboxPoll, Events: string(b)})
		}
		if c.DigestAt != "" {
			for _, e := range matched {
				b, err := json.Marshal(e)
				if err != nil {
					return nil, nil, err
				}
				pending = append(pending, store.PendingEvent{ChannelID: c.ID, Event: string(b)})
			}
		}
	}
	return outbox, pending, nil
}

// queueLongPauses notifies flip-flopping programs' pauses that have lasted
// LongPause (held back when they happened), once each; ones older than a
// FlipWindow are let go.
func (n *Notifier) queueLongPauses(ctx context.Context, now time.Time) error {
	changes, err := n.Store.LongPauses(ctx, now.Add(-LongPause), now.Add(-store.FlipWindow))
	if err != nil || len(changes) == 0 {
		return err
	}
	chans, err := n.channels(ctx)
	if err != nil {
		return err
	}
	outbox, pending, err := route(chans, FromChanges(changes))
	if err != nil {
		return err
	}
	ids := make([]int64, len(changes))
	for i, c := range changes {
		ids[i] = c.ID
	}
	return n.Store.QueueLongPauses(ctx, ids, outbox, pending)
}

// queueReportNotices queues news on the user's reports for the channels
// that choose it, as one message a channel. A poll saves reports a page at
// a time, so news waits for running polls to end (up to an hour), to come
// as one message rather than a few.
func (n *Notifier) queueReportNotices(ctx context.Context, now time.Time) error {
	notices, err := n.Store.ReportNotices(ctx)
	if err != nil || len(notices) == 0 {
		return err
	}
	if notices[0].OccurredAt.After(now.Add(-time.Hour)) {
		running, err := n.Store.RunningSince(ctx, now.Add(-time.Hour))
		if err != nil || len(running) > 0 {
			return err
		}
	}
	chans, err := n.channels(ctx)
	if err != nil {
		return err
	}
	outbox, pending, err := route(chans, FromReportNotices(notices))
	if err != nil {
		return err
	}
	ids := make([]int64, len(notices))
	for i, rn := range notices {
		ids[i] = rn.ID
	}
	return n.Store.QueueReportNotices(ctx, ids, outbox, pending)
}

// runEvents are a poll's events: its changes, and a platform alert when
// polls start or stop failing. Saving a program added by hand is a poll
// too, of nothing new to the user: it has none.
func (n *Notifier) runEvents(ctx context.Context, run store.Run) ([]Event, error) {
	if run.Platform == store.Independent {
		return nil, nil // the user's own edits to programs added by hand
	}
	changes, err := n.Store.NotifyChanges(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	events := FromChanges(changes)
	at := run.StartedAt
	if run.FinishedAt != nil {
		at = *run.FinishedAt
	}
	switch {
	case run.Status == store.RunFailed && run.Error == store.RunInterrupted:
	case run.Status == store.RunFailed:
		streak, err := n.Store.FailureStreak(ctx, run.Platform, run.ID)
		if err != nil {
			return nil, err
		}
		if streak == failureStreak {
			events = append(events, Event{Kind: EventPlatformProblem, At: at, Platform: run.Platform,
				Detail: fmt.Sprintf("The last %d polls failed: %s", streak, truncate(run.Error, 300))})
		}
	default:
		streak, err := n.Store.FailureStreak(ctx, run.Platform, run.ID-1)
		if err != nil {
			return nil, err
		}
		if streak >= failureStreak {
			events = append(events, Event{Kind: EventPlatformRecovered, At: at, Platform: run.Platform,
				Detail: fmt.Sprintf("A poll worked after %d failed ones.", streak)})
		}
	}
	return events, nil
}

// location is the zone digest times are in.
func (n *Notifier) location(ctx context.Context) *time.Location {
	loc, err := n.Store.Timezone(ctx, n.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// queueDigests queues each channel's digest once its time has come.
func (n *Notifier) queueDigests(ctx context.Context, now time.Time) error {
	chans, err := n.channels(ctx)
	if err != nil {
		return err
	}
	loc := n.location(ctx)
	for _, c := range chans {
		if c.DigestAt == "" {
			continue
		}
		slot := LastSlot(c.DigestAt, now, loc)
		if c.DigestSentAt != nil && !c.DigestSentAt.Before(slot) {
			continue
		}
		pending, err := n.Store.PendingEvents(ctx, c.ID)
		if err != nil {
			return err
		}
		var events []Event
		var through int64
		for _, p := range pending {
			var e Event
			if err := json.Unmarshal([]byte(p.Event), &e); err == nil {
				events = append(events, e)
			}
			through = p.ID
		}
		msg := store.OutboxMessage{ChannelID: c.ID, Kind: store.OutboxDigest}
		if events = merge(events); len(events) > 0 {
			b, err := json.Marshal(events)
			if err != nil {
				return err
			}
			msg.Events = string(b)
		}
		if err := n.Store.QueueDigest(ctx, c.ID, slot, msg, through); err != nil {
			return err
		}
	}
	return nil
}

// sendDue delivers each channel's next due message, recording progress so
// a retry never repeats a part that went out.
func (n *Notifier) sendDue(ctx context.Context, now time.Time) error {
	due, err := n.Store.DueOutbox(ctx, now)
	if err != nil || len(due) == 0 {
		return err
	}
	base := n.baseURL(ctx)
	loc := n.location(ctx)
	for _, m := range due {
		c, err := n.Store.NotifyChannel(ctx, m.ChannelID)
		if err != nil {
			return err
		}
		var events []Event
		if err := json.Unmarshal([]byte(m.Events), &events); err != nil {
			return n.Store.OutboxFailed(ctx, m.ID, time.Time{}, "unreadable message")
		}
		sent, err := n.deliver(ctx, *c, m.Kind, events, m.CreatedAt, m.PartsSent, base, loc)
		if sent > m.PartsSent && err != nil {
			if perr := n.Store.OutboxProgress(ctx, m.ID, sent); perr != nil {
				return perr
			}
		}
		if err == nil {
			if err := n.Store.OutboxSent(ctx, m.ID); err != nil {
				return err
			}
			continue
		}
		if ctx.Err() != nil {
			return nil // shutting down; the message stays queued
		}
		next := retryAt(now, m.Attempts+1, err)
		n.Log.Warn("notification not sent", "channel", c.Name, "attempt", m.Attempts+1, "err", err, "gave_up", next.IsZero())
		if err := n.Store.OutboxFailed(ctx, m.ID, next, err.Error()); err != nil {
			return err
		}
	}
	return nil
}

// retryAt is when to try again after attempt failed, or zero to give up.
func retryAt(now time.Time, attempt int, err error) time.Time {
	var se *sendError
	if errors.As(err, &se) && se.permanent || attempt >= maxAttempts {
		return time.Time{}
	}
	wait := time.Duration(math.Min(float64(time.Minute)*math.Pow(2, float64(attempt-1)), float64(3*time.Hour)))
	if errors.As(err, &se) && se.retryAfter > wait {
		wait = se.retryAfter
	}
	return now.Add(wait)
}

// deliver sends a message's parts from skip on, returning how many have
// gone out in all.
func (n *Notifier) deliver(ctx context.Context, c store.NotifyChannel, kind string, events []Event, at time.Time, skip int,
	base string, loc *time.Location) (int, error) {
	cfg := ParseConfig(c.Config)
	if c.Kind == store.NotifyWebhook {
		body, err := webhookBody(kind, events, at, base)
		if err != nil {
			return 0, err
		}
		if err := n.sendWebhook(ctx, cfg, kind, body); err != nil {
			return 0, err
		}
		return 1, nil
	}
	var parts []string
	var send func(context.Context, Config, string) error
	switch c.Kind {
	case store.NotifyDiscord:
		parts = renderer{st: discordStyle{}, base: base, loc: loc}.parts(kind, events, at, discordLimit)
		send = n.sendDiscord
	case store.NotifyTelegram:
		parts = renderer{st: telegramStyle{}, base: base, loc: loc}.parts(kind, events, at, telegramLimit)
		send = n.sendTelegram
	case store.NotifyBark:
		parts = renderer{st: plainStyle{}, base: base, loc: loc}.parts(kind, events, at, barkLimit)
		send = n.sendBark
	case store.NotifyServerChan:
		parts = renderer{st: discordStyle{}, base: base, loc: loc}.parts(kind, events, at, serverChanLimit)
		send = n.sendServerChan
	default:
		return 0, &sendError{msg: "unknown channel kind " + c.Kind, permanent: true}
	}
	for i := skip; i < len(parts); i++ {
		if err := send(ctx, cfg, parts[i]); err != nil {
			return i, err
		}
	}
	return len(parts), nil
}

// baseURL is HuntDeck's address for links in messages, or "".
func (n *Notifier) baseURL(ctx context.Context) string {
	set, err := n.Store.Settings(ctx, BaseURLSetting)
	if err != nil {
		return ""
	}
	return strings.TrimRight(set[BaseURLSetting], "/")
}

// Test sends a test message to a channel now, returning what went wrong.
func (n *Notifier) Test(ctx context.Context, c store.NotifyChannel) error {
	e := Event{Kind: EventTest, At: n.Store.Now(), Platform: "huntdeck"}
	_, err := n.deliver(ctx, c, store.OutboxTest, []Event{e}, e.At, 0, n.baseURL(ctx), n.location(ctx))
	return err
}

const (
	// barkLimit is what an iOS notification shows without a "more" tap;
	// serverChanLimit is WeChat's article body, which Markdown fills.
	barkLimit       = 3000
	serverChanLimit = 8000
)
