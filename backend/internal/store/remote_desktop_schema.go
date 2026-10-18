package store

// The computers Remote Desktop opens sessions to, and the sessions it opened.
// A computer's password is sealed with the master key like every other stored
// credential; the session history carries who connected and when, never what
// was on the screen. A computer paired with the stream bridge keeps the
// bridge's id for it, which is all that is needed to open a stream.
const remoteDesktopSchema = `
CREATE TABLE IF NOT EXISTS remote_desktop_computers (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  os TEXT NOT NULL,
  host TEXT NOT NULL,
  protocol TEXT NOT NULL,
  port INTEGER NOT NULL,
  username TEXT NOT NULL DEFAULT '',
  domain TEXT NOT NULL DEFAULT '',
  password_enc TEXT NOT NULL DEFAULT '',
  security TEXT NOT NULL DEFAULT '',
  layout TEXT NOT NULL DEFAULT '',
  peer_id TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  last_session_at INTEGER NOT NULL DEFAULT 0,
  stream_host_id INTEGER NOT NULL DEFAULT 0,
  gfx INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS remote_desktop_sessions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  computer_id TEXT NOT NULL REFERENCES remote_desktop_computers(id) ON DELETE CASCADE,
  actor TEXT NOT NULL,
  started_at INTEGER NOT NULL,
  ended_at INTEGER NOT NULL DEFAULT 0,
  outcome TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_remote_desktop_sessions_started ON remote_desktop_sessions(started_at);
CREATE INDEX IF NOT EXISTS idx_remote_desktop_sessions_computer ON remote_desktop_sessions(computer_id, started_at);
`
