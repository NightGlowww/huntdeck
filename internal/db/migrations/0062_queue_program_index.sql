-- The queue's "one per program" filter asks for each program's best target;
-- without this it scans the program's rows for every candidate (measured:
-- 780 ms for a page that should take 8).
CREATE INDEX hunt_queue_program_best ON hunt_queue (program_id, score DESC) WHERE gate = '';
