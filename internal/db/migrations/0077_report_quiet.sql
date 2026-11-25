-- When a gone-quiet notice was last sent for a report. The state is derived:
-- an undecided report with no word from the program for the days Settings ›
-- Reports sets. Without this column the notifier would either never tell the
-- user or tell them every hour the condition holds; with it, once per quiet
-- episode: a new word from the program is a newer last_activity_at, which
-- makes the report eligible again the next time it goes quiet.
ALTER TABLE my_reports ADD COLUMN quiet_notified_at TEXT;
