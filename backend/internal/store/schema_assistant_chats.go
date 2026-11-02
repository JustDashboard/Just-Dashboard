package store

// assistantChatSchema holds the dashboard assistant's saved chats
// (api/handlers_assistant_chats.go): a deployment's, a new deployment's and the
// server's conversations, saved after every step so they outlive a restart and
// can be carried on from anywhere. Kept apart from `schema` so the block is read
// in one place; it is concatenated into `schema`, so the same rules hold: CREATE
// ... IF NOT EXISTS only, and a column a table gains after it has shipped goes
// into addedColumns.
const assistantChatSchema = `
-- One row per chat, its owner's alone. conversation_enc is exactly what the
-- provider is sent, so the chat is carried on as it was, and title_enc its
-- name; both hold the person's words and the server's answers, so both are
-- sealed with the install's master key, as the assistant's keyring is. What
-- the page lists is beside them in the clear: what it is about, which model,
-- when, and what it has done. A chat goes with its owner and with the
-- deployment it is about.
CREATE TABLE IF NOT EXISTS assistant_chats (
  id               TEXT PRIMARY KEY,
  owner_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  scope_kind       TEXT NOT NULL,
  deployment_id    INTEGER REFERENCES deploy_projects(id) ON DELETE CASCADE,
  environment_id   INTEGER NOT NULL DEFAULT 0,
  draft_id         TEXT NOT NULL DEFAULT '',
  scope_name       TEXT NOT NULL DEFAULT '',
  scope_key        INTEGER NOT NULL DEFAULT 0,
  title_enc        TEXT NOT NULL DEFAULT '',
  provider         TEXT NOT NULL DEFAULT '',
  model            TEXT NOT NULL DEFAULT '',
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL,
  steps            INTEGER NOT NULL DEFAULT 0,
  changes          INTEGER NOT NULL DEFAULT 0,
  waiting          INTEGER NOT NULL DEFAULT 0,
  conversation_enc TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS assistant_chats_owner ON assistant_chats(owner_id, updated_at);

-- What the page was shown, in order: each save appends the lines since the
-- last as one sealed JSON array, so a step writes only what is new.
CREATE TABLE IF NOT EXISTS assistant_chat_lines (
  chat_id   TEXT NOT NULL REFERENCES assistant_chats(id) ON DELETE CASCADE,
  seq       INTEGER NOT NULL,
  lines_enc TEXT NOT NULL,
  PRIMARY KEY (chat_id, seq)
);
`
