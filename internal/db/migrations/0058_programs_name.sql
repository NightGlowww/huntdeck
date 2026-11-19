-- The Programs list and every A–Z picker order by lower(name); without an
-- index SQLite sorted all 1,300 programs for each page (about 18 ms), and
-- with one it walks the names and stops at the page's thirty.
CREATE INDEX IF NOT EXISTS programs_name ON programs (lower(name));
