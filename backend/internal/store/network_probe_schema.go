package store

// Dedicated rows and constraints keep machine replay counters, one-use
// enrollment and job completion atomic without altering human credentials.
const networkProbeSchema = `
CREATE TABLE IF NOT EXISTS network_probe_vantages (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  location TEXT NOT NULL DEFAULT '',
  placement TEXT NOT NULL DEFAULT 'external_host',
  scopes TEXT NOT NULL DEFAULT '[]',
  public_key TEXT NOT NULL DEFAULT '',
  enrollment_hash TEXT NOT NULL UNIQUE,
  enrollment_expires INTEGER NOT NULL DEFAULT 0,
  sequence INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL DEFAULT 0,
  enrolled_at INTEGER NOT NULL DEFAULT 0,
  last_seen INTEGER NOT NULL DEFAULT 0,
  last_ip TEXT NOT NULL DEFAULT '',
  revoked_at INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_network_probe_vantage_public_key
  ON network_probe_vantages(public_key) WHERE public_key <> '';
CREATE TABLE IF NOT EXISTS network_probe_checks (
  id TEXT PRIMARY KEY,
  vantage_id TEXT NOT NULL REFERENCES network_probe_vantages(id),
  request TEXT NOT NULL,
  nonce TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL DEFAULT 'queued',
  created_at INTEGER NOT NULL DEFAULT 0,
  expires_at INTEGER NOT NULL DEFAULT 0,
  leased_at INTEGER NOT NULL DEFAULT 0,
  completed_at INTEGER NOT NULL DEFAULT 0,
  result TEXT NOT NULL DEFAULT '',
  started_by TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_network_probe_checks_vantage ON network_probe_checks(vantage_id,created_at DESC);
`
