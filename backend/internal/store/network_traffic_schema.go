package store

// Traffic, shaping and connection records beside the interface samples:
// transfer budgets per device, the remote addresses blocked from the
// connection table with why and until when, the queue parameters read back
// right after a shaping change, and the congestion-control comparison taken
// when BBR is switched. Each is an observation or an
// operator's note about the host, never a copy of the firewall or spec.
const networkTrafficSchema = `
CREATE TABLE IF NOT EXISTS network_interface_quotas (
  iface TEXT PRIMARY KEY,
  period TEXT NOT NULL DEFAULT 'month',
  direction TEXT NOT NULL DEFAULT 'both',
  limit_bytes INTEGER NOT NULL DEFAULT 0,
  created_by TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS network_address_blocks (
  id TEXT PRIMARY KEY,
  address TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  comment TEXT NOT NULL DEFAULT '',
  incident_run_id TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL DEFAULT 0,
  expires_at INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL DEFAULT 'active',
  ended_at INTEGER NOT NULL DEFAULT 0,
  ended_by TEXT NOT NULL DEFAULT '',
  end_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_network_address_blocks_state ON network_address_blocks(state, expires_at);
CREATE TABLE IF NOT EXISTS network_shaping_applied (
  device TEXT PRIMARY KEY,
  entry TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT '',
  handle TEXT NOT NULL DEFAULT '',
  options TEXT NOT NULL DEFAULT '{}',
  applied_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS network_congestion_snapshots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at INTEGER NOT NULL DEFAULT 0,
  before_algorithm TEXT NOT NULL DEFAULT '',
  after_algorithm TEXT NOT NULL DEFAULT '',
  actor TEXT NOT NULL DEFAULT '',
  payload TEXT NOT NULL DEFAULT '{}'
);
`
