-- Rules a model read out of a program's policy, cached by the policy's hash so
-- the same text is never sent twice. The deterministic extraction in
-- internal/web/beforetest.go stays authoritative: these are shown beside it,
-- marked as a model's reading.
CREATE TABLE program_ai_rules (
  program_id  INTEGER PRIMARY KEY REFERENCES programs(id) ON DELETE CASCADE,
  policy_hash TEXT NOT NULL,
  rules       TEXT NOT NULL,
  model       TEXT NOT NULL,
  at          TEXT NOT NULL
);
