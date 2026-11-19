-- API tokens: a script's second key to the same single gate. Only the SHA-256 of
-- a token is stored, so a copy of the database cannot be replayed against it.
CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY,
  name         TEXT NOT NULL,
  digest       BLOB NOT NULL UNIQUE,
  created_at   TEXT NOT NULL,
  last_used_at TEXT,
  revoked_at   TEXT
);
