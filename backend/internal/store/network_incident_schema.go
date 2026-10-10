package store

// Network incidents are the Overview's attention findings over time: when each
// was first and last seen, when a successful reading no longer found it, and
// when its reading failed so its absence could not be judged. They are
// observations of the host, recorded as the Overview is read; they configure
// nothing.
const networkIncidentSchema = `
CREATE TABLE IF NOT EXISTS network_incidents (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 finding_id TEXT NOT NULL,
 source TEXT NOT NULL DEFAULT '',
 level TEXT NOT NULL DEFAULT '',
 title TEXT NOT NULL DEFAULT '',
 detail TEXT NOT NULL DEFAULT '',
 href TEXT NOT NULL DEFAULT '',
 opened_at INTEGER NOT NULL DEFAULT 0,
 last_seen_at INTEGER NOT NULL DEFAULT 0,
 resolved_at INTEGER NOT NULL DEFAULT 0,
 unobserved_since INTEGER NOT NULL DEFAULT 0,
 correlation TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_network_incidents_open ON network_incidents(resolved_at, finding_id);
CREATE INDEX IF NOT EXISTS idx_network_incidents_opened ON network_incidents(opened_at DESC, id);
`
