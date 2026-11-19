-- The factor breakdown of a queue row, computed when the queue is built, so the
-- page and the API can explain a score without re-running its inputs.
ALTER TABLE hunt_queue ADD COLUMN factors TEXT NOT NULL DEFAULT '[]';
