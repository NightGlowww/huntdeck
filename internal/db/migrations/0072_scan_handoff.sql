-- What HuntDeck has handed to a scanner, so a re-poll does not create the same
-- scan twice. One row per target; the task id is the receipt.
CREATE TABLE scan_handoffs (
    target     TEXT PRIMARY KEY,   -- the identifier as the platform lists it
    platform   TEXT NOT NULL,
    kind       TEXT NOT NULL DEFAULT '',
    task_id    TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

-- The scanner's settings: one row, because there is one scanner per install.
-- The endpoint and key are a config field (environment or Settings); these are
-- the choices that only make sense against that endpoint.
CREATE TABLE scan_config (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    enabled     INTEGER NOT NULL DEFAULT 0,
    template_id TEXT NOT NULL DEFAULT '',
    node_name   TEXT NOT NULL DEFAULT '',
    per_task    INTEGER NOT NULL DEFAULT 50,
    kinds       TEXT NOT NULL DEFAULT '',  -- comma separated; empty = every kind
    updated_at  TEXT NOT NULL
);
