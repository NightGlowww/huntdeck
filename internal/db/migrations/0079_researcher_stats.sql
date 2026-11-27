CREATE TABLE researcher_stats (
    platform    TEXT PRIMARY KEY,
    username    TEXT NOT NULL DEFAULT '',
    captured_at TEXT NOT NULL,
    data        TEXT NOT NULL
);
