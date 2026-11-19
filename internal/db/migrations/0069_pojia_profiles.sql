-- POJIA profiles cached per program: the disclosed-report history of a
-- program changes slowly, so a fetch is kept for a week rather than paying
-- the API on every page view. The API key itself never reaches this table.
CREATE TABLE pojia_profiles (
    program_id INTEGER PRIMARY KEY REFERENCES programs (id) ON DELETE CASCADE,
    handle     TEXT NOT NULL,
    profile    TEXT NOT NULL,    -- the tool's text answer, verbatim
    found      INTEGER NOT NULL, -- 1 when POJIA knows the vendor, 0 when not
    fetched_at TEXT NOT NULL,
    error      TEXT NOT NULL DEFAULT ''
);