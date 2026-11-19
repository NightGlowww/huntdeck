-- A hunter's notes on a program. They used to live in the browser
-- (localStorage), which lost them with a cache clear or a second device;
-- HuntDeck is single-user, so the server is where they belong.
CREATE TABLE program_notes (
    program_id INTEGER PRIMARY KEY REFERENCES programs (id) ON DELETE CASCADE,
    note       TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;
