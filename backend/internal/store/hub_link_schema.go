package store

// Every command a linked Just Hub has delivered, kept so the same signed
// command cannot run twice. It is account_link_commands under another name:
// the hub link is a second instance of the same engine, and the two links
// never share a replay record, so one link's ids can never block the other's.
const hubLinkSchema = `
CREATE TABLE IF NOT EXISTS hub_link_commands (
  id          TEXT PRIMARY KEY,
  kind        TEXT NOT NULL,
  received_at INTEGER NOT NULL,
  status      TEXT NOT NULL,
  finished_at INTEGER,
  error       TEXT NOT NULL DEFAULT '',
  issued_by   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_hub_link_commands_received ON hub_link_commands(received_at);
`
