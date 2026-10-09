package store

// Route history keeps periodic kernel observations: each row is a change
// seen between two snapshots, never a reconstruction of what happened in
// between. The single snapshot row lets a restarted process report changes
// made while it was not running, bounded by the last time it looked.
const networkRouteHistorySchema = `
CREATE TABLE IF NOT EXISTS network_route_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  observed_at INTEGER NOT NULL,
  previous_at INTEGER NOT NULL DEFAULT 0,
  across_restart INTEGER NOT NULL DEFAULT 0,
  object TEXT NOT NULL,
  change TEXT NOT NULL,
  family TEXT NOT NULL DEFAULT '',
  table_id INTEGER NOT NULL DEFAULT 0,
  table_name TEXT NOT NULL DEFAULT '',
  destination TEXT NOT NULL DEFAULT '',
  owner TEXT NOT NULL DEFAULT '',
  managed INTEGER NOT NULL DEFAULT 0,
  before_summary TEXT NOT NULL DEFAULT '',
  after_summary TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_network_route_events_at ON network_route_events(observed_at DESC);
CREATE TABLE IF NOT EXISTS network_route_snapshot (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  observed_at INTEGER NOT NULL,
  snapshot_json TEXT NOT NULL
);
`

// Firewall rule history records the dashboard's own firewall mutations under
// the stable identity of the rule they touched. Edits made with ufw or
// firewall-cmd directly leave no row; the page says so rather than implying
// a complete history.
const firewallHistorySchema = `
CREATE TABLE IF NOT EXISTS firewall_rule_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at INTEGER NOT NULL,
  actor TEXT NOT NULL DEFAULT '',
  backend TEXT NOT NULL DEFAULT '',
  operation TEXT NOT NULL,
  rule_id TEXT NOT NULL DEFAULT '',
  previous_rule_id TEXT NOT NULL DEFAULT '',
  rule_json TEXT NOT NULL DEFAULT '',
  previous_json TEXT NOT NULL DEFAULT '',
  outcome TEXT NOT NULL DEFAULT 'applied',
  detail TEXT NOT NULL DEFAULT '',
  change_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_firewall_rule_events_rule ON firewall_rule_events(rule_id, at DESC);
CREATE INDEX IF NOT EXISTS idx_firewall_rule_events_at ON firewall_rule_events(at DESC);
`
