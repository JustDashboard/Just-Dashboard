package store

// The WireGuard record beside the sealed client configurations: counter and
// handshake samples, daily usage, where each peer was seen from, what was done
// to each tunnel and the usage budgets an administrator set. Rows carry a
// peer's public key at most; private and preshared keys never reach them.
const networkWireGuardSchema = `
CREATE TABLE IF NOT EXISTS network_wg_samples (
  iface TEXT NOT NULL,
  public_key TEXT NOT NULL,
  ts INTEGER NOT NULL,
  rx INTEGER NOT NULL DEFAULT 0,
  tx INTEGER NOT NULL DEFAULT 0,
  rx_delta INTEGER NOT NULL DEFAULT 0,
  tx_delta INTEGER NOT NULL DEFAULT 0,
  span INTEGER NOT NULL DEFAULT 0,
  handshake INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (iface, public_key, ts)
);
CREATE INDEX IF NOT EXISTS idx_network_wg_samples_ts ON network_wg_samples(ts);
CREATE TABLE IF NOT EXISTS network_wg_usage (
  iface TEXT NOT NULL,
  public_key TEXT NOT NULL,
  day INTEGER NOT NULL,
  rx INTEGER NOT NULL DEFAULT 0,
  tx INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (iface, public_key, day)
);
CREATE TABLE IF NOT EXISTS network_wg_endpoints (
  iface TEXT NOT NULL,
  public_key TEXT NOT NULL,
  endpoint TEXT NOT NULL,
  first_seen INTEGER NOT NULL DEFAULT 0,
  last_seen INTEGER NOT NULL DEFAULT 0,
  observations INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY (iface, public_key, endpoint)
);
CREATE TABLE IF NOT EXISTS network_wg_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  iface TEXT NOT NULL,
  ts INTEGER NOT NULL DEFAULT 0,
  kind TEXT NOT NULL,
  outcome TEXT NOT NULL DEFAULT 'ok',
  peer_key TEXT NOT NULL DEFAULT '',
  peer_name TEXT NOT NULL DEFAULT '',
  actor TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_network_wg_events_iface ON network_wg_events(iface, ts);
CREATE TABLE IF NOT EXISTS network_wg_quotas (
  iface TEXT NOT NULL,
  public_key TEXT NOT NULL,
  period TEXT NOT NULL DEFAULT 'month',
  limit_bytes INTEGER NOT NULL DEFAULT 0,
  created_by TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (iface, public_key)
);
`
