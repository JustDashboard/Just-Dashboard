package store

// Gateway counters survive table replacement: each row keeps what earlier
// generations of the gateway table counted, and the last live reading of the
// current one. Samples hold bounded per-interval deltas and conntrack gauges
// for the Protection page's history; they are telemetry, not configuration.
const networkGatewaySchema = `
CREATE TABLE IF NOT EXISTS network_gateway_counters (
 key TEXT PRIMARY KEY,
 packets INTEGER NOT NULL DEFAULT 0,
 bytes INTEGER NOT NULL DEFAULT 0,
 live_packets INTEGER NOT NULL DEFAULT 0,
 live_bytes INTEGER NOT NULL DEFAULT 0,
 generation INTEGER NOT NULL DEFAULT 0,
 since INTEGER NOT NULL DEFAULT 0,
 observed_at INTEGER NOT NULL DEFAULT 0,
 resets INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS network_protection_samples (
 ts INTEGER NOT NULL,
 key TEXT NOT NULL,
 value INTEGER NOT NULL DEFAULT 0,
 bytes INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY (ts, key)
);
CREATE INDEX IF NOT EXISTS idx_network_protection_samples_key ON network_protection_samples(key, ts);
`
