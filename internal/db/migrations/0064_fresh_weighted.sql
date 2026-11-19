-- Freshness's contribution to the total, not only its part score: the queue's
-- quality input subtracts the contribution, and computing that in SQL left the
-- weight out of reach (internal/store cannot import internal/huntscore).
ALTER TABLE hunt_scores ADD COLUMN fresh_weighted REAL;
