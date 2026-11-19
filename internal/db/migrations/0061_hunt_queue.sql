-- HuntQueue: the ranked answer to "can I start today, what is it worth".
-- Derived data like target_scores, so it is rebuilt rather than seeded.
CREATE TABLE hunt_queue (
    program_id INTEGER NOT NULL REFERENCES programs (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    value      TEXT NOT NULL,
    apex       TEXT NOT NULL,
    score      REAL NOT NULL,
    gate       TEXT NOT NULL, -- empty when ranked, else why it is not
    reasons    TEXT NOT NULL, -- JSON array of strings
    computed_at TEXT NOT NULL,
    PRIMARY KEY (program_id, kind, value)
) STRICT;

CREATE INDEX hunt_queue_best ON hunt_queue (score DESC);

-- The page reads the top rows by score; the gate list is read separately.
CREATE INDEX hunt_queue_gate ON hunt_queue (gate) WHERE gate <> '';
