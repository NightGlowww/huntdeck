-- MCP keys are their own thing on their own page: same gate, same digest
-- storage, but a kind of their own so the MCP page manages only its keys and
-- the API page only its tokens.
ALTER TABLE api_tokens ADD COLUMN kind TEXT NOT NULL DEFAULT 'api';
