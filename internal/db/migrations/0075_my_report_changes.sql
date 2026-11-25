-- What syncs found new on the user's own reports (a state, a severity, a
-- bounty, the program waiting on the user), waiting for the notifier to
-- queue it; deleted once queued. Kept apart from changes, which seeds copy:
-- like my_reports, it's the user's and stays out of seeds and out of a
-- read-only copy.
CREATE TABLE my_report_changes (
    id          INTEGER PRIMARY KEY,
    report_id   INTEGER NOT NULL REFERENCES my_reports (id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,                 -- new, state, severity, bounty, waiting
    from_value  TEXT NOT NULL DEFAULT '',
    to_value    TEXT NOT NULL DEFAULT '',
    amount      REAL,
    currency    TEXT NOT NULL DEFAULT '',
    occurred_at TEXT NOT NULL
);
