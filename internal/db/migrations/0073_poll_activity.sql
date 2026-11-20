-- A running poll's counters are written when it ends, so a row that is running
-- says nothing about whether it is alive: two polls sat at zero requests for
-- twenty minutes and looked hung when they were working. This column is touched
-- every few seconds while a run works, so anything reading the database - the
-- doctor, a script, another process - can tell working from stuck.
ALTER TABLE poll_runs ADD COLUMN last_activity TEXT;
