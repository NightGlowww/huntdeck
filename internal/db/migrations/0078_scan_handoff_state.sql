-- What the scanner said about a handoff's task, so the pipeline closes:
-- until now HuntDeck knew it had handed targets over and never learned the
-- outcome, and the one open link in docs/pipeline.md. status is the
-- scanner's own number; state is our reading of it (queued, running, done,
-- failed, unknown) and checked_at when it was read.
ALTER TABLE scan_handoffs ADD COLUMN state TEXT NOT NULL DEFAULT '';
ALTER TABLE scan_handoffs ADD COLUMN status INTEGER NOT NULL DEFAULT -1;
ALTER TABLE scan_handoffs ADD COLUMN checked_at TEXT;

-- A reconcile reads the newest handoffs first.
CREATE INDEX scan_handoffs_checked ON scan_handoffs (checked_at);
