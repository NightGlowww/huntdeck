-- What the queue's rebuild left out, so the page can say it rather than
-- silently showing a shorter list: today, the targets whose score fell below
-- the floor that makes one worth looking at.
CREATE TABLE queue_dropped (
  day          TEXT PRIMARY KEY,
  below_floor  INTEGER NOT NULL,
  computed_at  TEXT NOT NULL
);
