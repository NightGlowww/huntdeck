-- The user's own reports (Reports and Analytics): read from the platforms
-- with the logins HuntDeck already has, or added by hand. Metadata only:
-- never the write-up and never comment text (comments are counted). All
-- three tables are the user's and stay out of seeds (store/seedspec.go)
-- and out of a read-only copy (store.CheckSignedOut).
CREATE TABLE my_reports (
    id                        INTEGER PRIMARY KEY,
    platform                  TEXT NOT NULL,
    report_key                TEXT NOT NULL,           -- the platform's ID; "manual-N" for ones added by hand
    program_id                INTEGER REFERENCES programs (id) ON DELETE SET NULL,
    program_handle            TEXT NOT NULL DEFAULT '',
    program_name              TEXT NOT NULL DEFAULT '',
    title                     TEXT NOT NULL DEFAULT '',
    url                       TEXT NOT NULL DEFAULT '',
    state                     TEXT NOT NULL,           -- reports.State
    raw_state                 TEXT NOT NULL DEFAULT '', -- the platform's own words for it
    severity                  TEXT NOT NULL DEFAULT '', -- none, low, medium, high, critical; '' unrated
    severity_label            TEXT NOT NULL DEFAULT '', -- the platform's (P2, Exceptional), when it differs
    submitted_severity        TEXT NOT NULL DEFAULT '', -- what the hunter submitted it as; '' unknown
    severity_changed          INTEGER NOT NULL DEFAULT 0, -- triage changed it (known even when the original isn't)
    cvss_score                REAL,
    submitted_at              TEXT NOT NULL,
    first_response_at         TEXT,
    triaged_at                TEXT,
    first_bounty_at           TEXT,
    closed_at                 TEXT,
    last_activity_at          TEXT,
    last_program_activity_at  TEXT,
    last_hunter_activity_at   TEXT,
    waiting_on                TEXT NOT NULL DEFAULT '', -- program, you, ''
    comments                  INTEGER NOT NULL DEFAULT 0,
    unread                    INTEGER NOT NULL DEFAULT 0,
    duplicate_of              TEXT NOT NULL DEFAULT '',
    source                    TEXT NOT NULL DEFAULT 'platform', -- platform, manual
    marker                    TEXT NOT NULL DEFAULT '', -- the change marker the details were last read at
    detail_at                 TEXT,
    payments_at               TEXT,                    -- when its payments were last read whole
    gone_at                   TEXT,                    -- missing from a complete listing (never deleted)
    snoozed_until             TEXT,
    muted                     INTEGER NOT NULL DEFAULT 0,
    edits                     TEXT NOT NULL DEFAULT '{}', -- JSON: fields the user set by hand, which syncs keep
    raw                       TEXT NOT NULL DEFAULT '{}', -- allow-listed platform fields, never a write-up
    first_seen_at             TEXT NOT NULL,
    updated_at                TEXT NOT NULL,
    UNIQUE (platform, report_key)
) STRICT;

CREATE INDEX my_reports_program ON my_reports (program_id);
CREATE INDEX my_reports_submitted ON my_reports (submitted_at);

-- Money a report earned: bounties, bonuses and retests, in the currency
-- paid (never converted).
CREATE TABLE my_report_payments (
    id         INTEGER PRIMARY KEY,
    report_id  INTEGER NOT NULL REFERENCES my_reports (id) ON DELETE CASCADE,
    ref        TEXT NOT NULL DEFAULT '', -- the platform's ID for it, when it has one
    kind       TEXT NOT NULL,            -- bounty, bonus, retest, other
    amount     REAL NOT NULL,
    currency   TEXT NOT NULL,
    awarded_at TEXT NOT NULL,
    paid_at    TEXT,
    status     TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX my_report_payments_report ON my_report_payments (report_id);
CREATE INDEX my_report_payments_awarded ON my_report_payments (awarded_at);

-- A report's timeline as the platform shows it: state and severity changes,
-- bounties, and who commented when. No text.
CREATE TABLE my_report_events (
    report_id INTEGER NOT NULL REFERENCES my_reports (id) ON DELETE CASCADE,
    at        TEXT NOT NULL,
    kind      TEXT NOT NULL, -- state, severity, bounty, comment_program, comment_you
    from_value TEXT NOT NULL DEFAULT '',
    to_value   TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX my_report_events_report ON my_report_events (report_id, at);
