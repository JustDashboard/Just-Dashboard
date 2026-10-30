package store

// Every command a linked site has delivered, kept so the same signed command
// cannot run twice. The id is the site's own ULID, so a replay hits the primary
// key before anything executes. A row is written when the command passes its
// signature and left as 'running' until it finishes; one whose process died
// meanwhile is closed as failed the next time the connector starts.
const accountLinkSchema = `
CREATE TABLE IF NOT EXISTS account_link_commands (
  id          TEXT PRIMARY KEY,
  kind        TEXT NOT NULL,
  received_at INTEGER NOT NULL,
  status      TEXT NOT NULL,
  finished_at INTEGER,
  error       TEXT NOT NULL DEFAULT '',
  issued_by   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_account_link_commands_received ON account_link_commands(received_at);
`
