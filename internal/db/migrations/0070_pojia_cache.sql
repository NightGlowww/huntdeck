-- POJIA answers cached by the call that produced them: a search, a report,
-- a payload set. Search answers age in hours, the rest in days; the key is
-- the tool name plus its arguments, hashed.
CREATE TABLE pojia_cache (
    key        TEXT PRIMARY KEY,
    answer     TEXT NOT NULL,
    fetched_at TEXT NOT NULL
);
