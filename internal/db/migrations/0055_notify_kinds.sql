-- Bark (iOS push) and ServerChan (WeChat push) join the notification
-- channel kinds. SQLite cannot change a CHECK in place, so
-- notify_channels is rebuilt; its children cascade on delete, so their
-- rows are kept aside and put back afterwards.
CREATE TEMP TABLE pending_keep AS SELECT id, channel_id, event, created_at FROM notify_pending;
CREATE TEMP TABLE outbox_keep AS SELECT id, channel_id, kind, events, created_at, next_at, attempts, parts_sent, sent_at, failed_at, last_error FROM notify_outbox;

CREATE TABLE notify_channels_new (
    id             INTEGER PRIMARY KEY,
    name           TEXT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('discord', 'telegram', 'webhook', 'bark', 'serverchan')),
    config         TEXT NOT NULL,
    rules          TEXT NOT NULL,
    enabled        INTEGER NOT NULL DEFAULT 1,
    instant        INTEGER NOT NULL DEFAULT 1,
    digest_at      TEXT NOT NULL DEFAULT '',
    digest_sent_at TEXT,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
) STRICT;
INSERT INTO notify_channels_new (id, name, kind, config, rules, enabled, instant, digest_at, digest_sent_at, created_at, updated_at)
SELECT id, name, kind, config, rules, enabled, instant, digest_at, digest_sent_at, created_at, updated_at FROM notify_channels;
DROP TABLE notify_channels;
ALTER TABLE notify_channels_new RENAME TO notify_channels;

INSERT INTO notify_pending (id, channel_id, event, created_at)
SELECT id, channel_id, event, created_at FROM pending_keep;
INSERT INTO notify_outbox (id, channel_id, kind, events, created_at, next_at, attempts, parts_sent, sent_at, failed_at, last_error)
SELECT id, channel_id, kind, events, created_at, next_at, attempts, parts_sent, sent_at, failed_at, last_error FROM outbox_keep;
DROP TABLE temp.pending_keep;
DROP TABLE temp.outbox_keep;

