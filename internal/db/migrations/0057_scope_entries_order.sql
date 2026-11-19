-- The Targets page's "newest first" order walks entries by when they were
-- added. Without an index on that order SQLite sorted every matching target
-- (39k domains: ~250 ms here, ~600 ms on a loaded instance); with it, and an
-- entries-first join, it stops after the first hundred. The expression puts
-- undated entries last, exactly as the page's ORDER BY does.
CREATE INDEX IF NOT EXISTS scope_entries_order ON scope_entries ((added_at IS NULL), added_at DESC);
