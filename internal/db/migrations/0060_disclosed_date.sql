-- The Disclosed list is newest first, and with no index on that order SQLite
-- sorted all 11,000 reports for every page (9 ms at the top, 53 ms twenty
-- pages in). With it the list walks the dates and stops.
CREATE INDEX IF NOT EXISTS disclosed_reports_date ON disclosed_reports (disclosed_at DESC);
