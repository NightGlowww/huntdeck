package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Notification channel kinds.
const (
	NotifyDiscord    = "discord"
	NotifyTelegram   = "telegram"
	NotifyWebhook    = "webhook"
	NotifyBark       = "bark"       // iOS push, Bark or a self-hosted bark-server
	NotifyServerChan = "serverchan" // WeChat push, ServerChan (Server酱)
)

// Outbox message kinds.
const (
	OutboxPoll   = "poll"
	OutboxDigest = "digest"
	OutboxTest   = "test"
)

// NotifyChannel is a place notifications go, with what to send there.
// Config and Rules are JSON the notify package defines.
type NotifyChannel struct {
	ID           int64
	Name         string
	Kind         string
	Config       string
	Rules        string
	Enabled      bool
	Instant      bool       // send each poll's events as it finishes
	DigestAt     string     // "HH:MM" in the user's time zone, "" for no digest
	DigestSentAt *time.Time // the last digest slot handled
	CreatedAt    time.Time
	UpdatedAt    time.Time

	// Delivery status, for Settings.
	LastSentAt   *time.Time
	LastFailedAt *time.Time
	LastError    string
	Queued       int // messages not sent yet
	Pending      int // events waiting for the digest
}

const selectNotifyChannels = `
	SELECT c.id, c.name, c.kind, c.config, c.rules, c.enabled, c.instant, c.digest_at, c.digest_sent_at, c.created_at, c.updated_at,
		(SELECT MAX(sent_at) FROM notify_outbox o WHERE o.channel_id = c.id),
		(SELECT MAX(failed_at) FROM notify_outbox o WHERE o.channel_id = c.id),
		COALESCE((SELECT last_error FROM notify_outbox o WHERE o.channel_id = c.id AND o.last_error <> '' ORDER BY o.id DESC LIMIT 1), ''),
		(SELECT COUNT(*) FROM notify_outbox o WHERE o.channel_id = c.id AND o.sent_at IS NULL AND o.failed_at IS NULL),
		(SELECT COUNT(*) FROM notify_pending n WHERE n.channel_id = c.id)
	FROM notify_channels c`

// NotifyChannels returns every channel, by name.
func (s *Store) NotifyChannels(ctx context.Context) ([]NotifyChannel, error) {
	rows, err := s.db.R.QueryContext(ctx, selectNotifyChannels+` ORDER BY lower(c.name), c.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []NotifyChannel
	for rows.Next() {
		c, err := scanNotifyChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// NotifyChannel returns one channel, or ErrNotFound.
func (s *Store) NotifyChannel(ctx context.Context, id int64) (*NotifyChannel, error) {
	c, err := scanNotifyChannel(s.db.R.QueryRowContext(ctx, selectNotifyChannels+` WHERE c.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func scanNotifyChannel(row interface{ Scan(...any) error }) (NotifyChannel, error) {
	var c NotifyChannel
	var digestSent, sent, failed sql.NullString
	var created, updated string
	err := row.Scan(&c.ID, &c.Name, &c.Kind, &c.Config, &c.Rules, &c.Enabled, &c.Instant, &c.DigestAt, &digestSent,
		&created, &updated, &sent, &failed, &c.LastError, &c.Queued, &c.Pending)
	if err != nil {
		return c, err
	}
	c.DigestSentAt, c.LastSentAt, c.LastFailedAt = parseTSPtr(digestSent), parseTSPtr(sent), parseTSPtr(failed)
	c.CreatedAt, c.UpdatedAt = parseTS(created), parseTS(updated)
	return c, nil
}

// SaveNotifyChannel inserts c (ID 0) or updates it, returning its ID.
// Changing the digest time doesn't resend today's digest.
func (s *Store) SaveNotifyChannel(ctx context.Context, c NotifyChannel) (int64, error) {
	id := c.ID
	err := s.Write(ctx, func(tx *Tx) error {
		if id == 0 {
			res, err := tx.tx.ExecContext(ctx, `
				INSERT INTO notify_channels (name, kind, config, rules, enabled, instant, digest_at, digest_sent_at, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				c.Name, c.Kind, c.Config, c.Rules, b2i(c.Enabled), b2i(c.Instant), c.DigestAt, tsPtr(c.DigestSentAt), tx.now, tx.now)
			if err != nil {
				return err
			}
			id, err = res.LastInsertId()
			return err
		}
		res, err := tx.tx.ExecContext(ctx, `
			UPDATE notify_channels SET name = ?, config = ?, rules = ?, enabled = ?, instant = ?, digest_at = ?,
				digest_sent_at = COALESCE(?, digest_sent_at), updated_at = ? WHERE id = ?`,
			c.Name, c.Config, c.Rules, b2i(c.Enabled), b2i(c.Instant), c.DigestAt, tsPtr(c.DigestSentAt), tx.now, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if c.DigestAt == "" {
			_, err = tx.tx.ExecContext(ctx, `DELETE FROM notify_pending WHERE channel_id = ?`, id)
		}
		return err
	})
	return id, err
}

// DeleteNotifyChannel removes a channel and everything queued for it.
func (s *Store) DeleteNotifyChannel(ctx context.Context, id int64) error {
	return s.Write(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `DELETE FROM notify_channels WHERE id = ?`, id)
		return err
	})
}

// UnnotifiedRuns returns finished polls whose changes haven't been turned
// into notifications yet, oldest first.
func (s *Store) UnnotifiedRuns(ctx context.Context) ([]Run, error) {
	return s.queryRuns(ctx, `WHERE notified_at IS NULL AND status <> 'running' ORDER BY id`)
}

// FailureStreak counts the platform's failed polls in a row, ending with
// run (inclusive); polls stopped by shutdown don't count either way.
func (s *Store) FailureStreak(ctx context.Context, platformName string, runID int64) (int, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT status FROM poll_runs WHERE platform = ? AND id <= ? AND status <> 'running'
			AND NOT (status = 'failed' AND error = ?)
		ORDER BY id DESC LIMIT 50`, platformName, runID, RunInterrupted)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return 0, err
		}
		if status != RunFailed {
			break
		}
		n++
	}
	return n, rows.Err()
}

// OutboxMessage is a message to queue for a channel; Events is a JSON array.
type OutboxMessage struct {
	ChannelID int64
	Kind      string
	Events    string
}

// PendingEvent is an event (JSON) waiting for a channel's digest.
type PendingEvent struct {
	ID        int64
	ChannelID int64
	Event     string
}

// QueueRun queues a finished poll's notifications and marks it notified,
// in one transaction, so each poll is notified exactly once.
func (s *Store) QueueRun(ctx context.Context, runID int64, outbox []OutboxMessage, pending []PendingEvent) error {
	return s.Write(ctx, func(tx *Tx) error {
		if err := tx.queue(ctx, outbox, pending); err != nil {
			return err
		}
		_, err := tx.tx.ExecContext(ctx, `UPDATE poll_runs SET notified_at = ? WHERE id = ?`, tx.now, runID)
		return err
	})
}

// queue adds messages to the outbox and events to digests.
func (t *Tx) queue(ctx context.Context, outbox []OutboxMessage, pending []PendingEvent) error {
	for _, m := range outbox {
		if err := t.queueOutbox(ctx, m); err != nil {
			return err
		}
	}
	for _, p := range pending {
		if _, err := t.tx.ExecContext(ctx, `INSERT INTO notify_pending (channel_id, event, created_at) VALUES (?, ?, ?)`,
			p.ChannelID, p.Event, t.now); err != nil {
			return err
		}
	}
	return nil
}

func (t *Tx) queueOutbox(ctx context.Context, m OutboxMessage) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO notify_outbox (channel_id, kind, events, created_at, next_at) VALUES (?, ?, ?, ?, ?)`,
		m.ChannelID, m.Kind, m.Events, t.now, t.now)
	return err
}

// QueueOutbox queues one message (a test, say).
func (s *Store) QueueOutbox(ctx context.Context, m OutboxMessage) error {
	return s.Write(ctx, func(tx *Tx) error { return tx.queueOutbox(ctx, m) })
}

// PendingEvents returns the events waiting for a channel's digest, oldest
// first.
func (s *Store) PendingEvents(ctx context.Context, channelID int64) ([]PendingEvent, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, channel_id, event FROM notify_pending WHERE channel_id = ? ORDER BY id`, channelID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PendingEvent
	for rows.Next() {
		var p PendingEvent
		if err := rows.Scan(&p.ID, &p.ChannelID, &p.Event); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// QueueDigest records that a channel's digest slot was handled: it queues
// msg (unless Events is empty) and drops the pending events up to
// throughID, in one transaction.
func (s *Store) QueueDigest(ctx context.Context, channelID int64, slot time.Time, msg OutboxMessage, throughID int64) error {
	return s.Write(ctx, func(tx *Tx) error {
		if msg.Events != "" {
			if err := tx.queueOutbox(ctx, msg); err != nil {
				return err
			}
		}
		if _, err := tx.tx.ExecContext(ctx, `DELETE FROM notify_pending WHERE channel_id = ? AND id <= ?`, channelID, throughID); err != nil {
			return err
		}
		_, err := tx.tx.ExecContext(ctx, `UPDATE notify_channels SET digest_sent_at = ? WHERE id = ?`, ts(slot), channelID)
		return err
	})
}

// OutboxItem is a queued message.
type OutboxItem struct {
	ID        int64
	ChannelID int64
	Kind      string
	Events    string
	CreatedAt time.Time
	Attempts  int
	PartsSent int
}

// DueOutbox returns, for each channel, its oldest unsent message if it's
// due by now: a channel's messages go out in order, and one waiting to be
// retried holds back the ones after it.
func (s *Store) DueOutbox(ctx context.Context, now time.Time) ([]OutboxItem, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT o.id, o.channel_id, o.kind, o.events, o.created_at, o.attempts, o.parts_sent
		FROM notify_outbox o JOIN notify_channels c ON c.id = o.channel_id
		WHERE c.enabled = 1 AND o.next_at <= ? AND o.id = (SELECT MIN(o2.id) FROM notify_outbox o2
			WHERE o2.channel_id = o.channel_id AND o2.sent_at IS NULL AND o2.failed_at IS NULL)
		ORDER BY o.id`, ts(now))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []OutboxItem
	for rows.Next() {
		var o OutboxItem
		var created string
		if err := rows.Scan(&o.ID, &o.ChannelID, &o.Kind, &o.Events, &created, &o.Attempts, &o.PartsSent); err != nil {
			return nil, err
		}
		o.CreatedAt = parseTS(created)
		out = append(out, o)
	}
	return out, rows.Err()
}

// OutboxProgress records that the first parts of a message went out.
func (s *Store) OutboxProgress(ctx context.Context, id int64, partsSent int) error {
	return s.Write(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `UPDATE notify_outbox SET parts_sent = ? WHERE id = ?`, partsSent, id)
		return err
	})
}

// OutboxSent marks a message delivered.
func (s *Store) OutboxSent(ctx context.Context, id int64) error {
	return s.Write(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `UPDATE notify_outbox SET sent_at = ?, attempts = attempts + 1 WHERE id = ?`, tx.now, id)
		return err
	})
}

// OutboxFailed records a failed attempt: retry at next, or give up (zero
// next) after too many.
func (s *Store) OutboxFailed(ctx context.Context, id int64, next time.Time, errText string) error {
	if len(errText) > 300 {
		errText = errText[:300]
	}
	return s.Write(ctx, func(tx *Tx) error {
		if next.IsZero() {
			_, err := tx.tx.ExecContext(ctx, `UPDATE notify_outbox SET attempts = attempts + 1, failed_at = ?, last_error = ? WHERE id = ?`,
				tx.now, errText, id)
			return err
		}
		_, err := tx.tx.ExecContext(ctx, `UPDATE notify_outbox SET attempts = attempts + 1, next_at = ?, last_error = ? WHERE id = ?`,
			ts(next), errText, id)
		return err
	})
}

// PruneOutbox drops delivered and abandoned messages older than before.
func (s *Store) PruneOutbox(ctx context.Context, before time.Time) error {
	return s.Write(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `DELETE FROM notify_outbox WHERE COALESCE(sent_at, failed_at) < ?`, ts(before))
		return err
	})
}

// Delivery is a recent outbox message, for Settings.
type Delivery struct {
	ID          int64
	ChannelName string
	Kind        string
	Events      int
	CreatedAt   time.Time
	SentAt      *time.Time
	FailedAt    *time.Time
	Attempts    int
	LastError   string
}

// RecentDeliveries returns the newest messages across channels.
func (s *Store) RecentDeliveries(ctx context.Context, limit int) ([]Delivery, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT o.id, c.name, o.kind, json_array_length(o.events), o.created_at, o.sent_at, o.failed_at, o.attempts, o.last_error
		FROM notify_outbox o JOIN notify_channels c ON c.id = o.channel_id
		ORDER BY o.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Delivery
	for rows.Next() {
		var d Delivery
		var created string
		var sent, failed sql.NullString
		if err := rows.Scan(&d.ID, &d.ChannelName, &d.Kind, &d.Events, &created, &sent, &failed, &d.Attempts, &d.LastError); err != nil {
			return nil, fmt.Errorf("delivery: %w", err)
		}
		d.CreatedAt, d.SentAt, d.FailedAt = parseTS(created), parseTSPtr(sent), parseTSPtr(failed)
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeliveryFilter narrows the delivery history: a channel's name (as the
// page shows it), a status ("sent", "failed" or "waiting"), and a limit.
type DeliveryFilter struct {
	Channel string
	Status  string
	Limit   int
}

// Deliveries lists recent deliveries newest first, filtered.
func (s *Store) Deliveries(ctx context.Context, f DeliveryFilter) ([]Delivery, error) {
	conds := []string{}
	args := []any{}
	if f.Channel != "" {
		conds = append(conds, "c.name = ?")
		args = append(args, f.Channel)
	}
	switch f.Status {
	case "sent":
		conds = append(conds, "o.sent_at IS NOT NULL")
	case "failed":
		conds = append(conds, "o.failed_at IS NOT NULL")
	case "waiting":
		conds = append(conds, "o.sent_at IS NULL AND o.failed_at IS NULL")
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	q := "SELECT o.id, c.name, o.kind, json_array_length(o.events), o.created_at, o.sent_at, o.failed_at, o.attempts, o.last_error" +
		" FROM notify_outbox o JOIN notify_channels c ON c.id = o.channel_id" + where + " ORDER BY o.id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.R.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Delivery
	for rows.Next() {
		var d Delivery
		var created string
		var sent, failed sql.NullString
		if err := rows.Scan(&d.ID, &d.ChannelName, &d.Kind, &d.Events, &created, &sent, &failed, &d.Attempts, &d.LastError); err != nil {
			return nil, fmt.Errorf("delivery: %w", err)
		}
		d.CreatedAt, d.SentAt, d.FailedAt = parseTS(created), parseTSPtr(sent), parseTSPtr(failed)
		out = append(out, d)
	}
	return out, rows.Err()
}

// NotificationBacklog counts the notifications queued and not yet delivered.
func (s *Store) NotificationBacklog(ctx context.Context) (int, error) {
	var n int
	err := s.db.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM notify_outbox WHERE sent_at IS NULL AND failed_at IS NULL").Scan(&n)
	return n, err
}
