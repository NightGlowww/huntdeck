-- The user's HuntMail addresses, by program (the Email page and a
-- program's Email tab). The mail itself stays in HuntMail; HuntDeck only
-- remembers which addresses it made for which program. Each program that
-- has some gets one prefix, letters and digits only and unique, so
-- "<prefix>-" finds its mail and never another program's. The user's own:
-- left out of seeds and of a read-only copy.
CREATE TABLE mail_prefixes (
    program_id INTEGER PRIMARY KEY REFERENCES programs (id) ON DELETE CASCADE,
    prefix     TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);

CREATE TABLE mail_addresses (
    id         INTEGER PRIMARY KEY,
    program_id INTEGER NOT NULL REFERENCES programs (id) ON DELETE CASCADE,
    address    TEXT NOT NULL UNIQUE,   -- lowercased
    note       TEXT NOT NULL DEFAULT '', -- the user's: what it's for ("second account")
    created_at TEXT NOT NULL
);
CREATE INDEX mail_addresses_program ON mail_addresses (program_id, created_at);
