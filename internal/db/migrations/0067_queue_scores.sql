-- The queue's head, once a day, so a later backtest can ask what it would have
-- picked: hunt_queue itself is replaced on every rebuild and keeps no history.
CREATE TABLE queue_scores (
  day        TEXT NOT NULL,
  program_id INTEGER NOT NULL REFERENCES programs(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL,
  value      TEXT NOT NULL,
  score      REAL NOT NULL,
  PRIMARY KEY (day, program_id, kind, value)
);
CREATE INDEX queue_scores_day ON queue_scores(day);
