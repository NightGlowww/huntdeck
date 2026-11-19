-- The Programs page's default order is by HuntScore, ties A to Z; without an
-- index SQLite sorted every program on each page (about 15 ms with 1,300).
-- The expression first puts unscored programs last, as the page does.
CREATE INDEX IF NOT EXISTS programs_score ON programs ((hunt_score IS NULL), hunt_score DESC, lower(name));
