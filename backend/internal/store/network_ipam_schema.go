package store

// IPAM holds planning metadata. Native resources remain owned by their
// existing managers; neither a reservation nor an observed ID adopts one.
const networkIPAMSchema = `
CREATE TABLE IF NOT EXISTS network_ipam_pools (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL DEFAULT '',
 prefix TEXT NOT NULL UNIQUE,
 allocation_bits INTEGER NOT NULL DEFAULT 0,
 created_at INTEGER NOT NULL DEFAULT 0,
 retired_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS network_ipam_reservations (
 id TEXT PRIMARY KEY,
 pool_id TEXT NOT NULL REFERENCES network_ipam_pools(id),
 prefix TEXT NOT NULL,
 owner TEXT NOT NULL DEFAULT '',
 resource TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL DEFAULT 'reserved',
 acknowledged_unknown INTEGER NOT NULL DEFAULT 0,
 unknown_sources TEXT NOT NULL DEFAULT '[]',
 created_at INTEGER NOT NULL DEFAULT 0,
 updated_at INTEGER NOT NULL DEFAULT 0,
 released_at INTEGER NOT NULL DEFAULT 0,
 started_by TEXT NOT NULL DEFAULT '',
 handoff_id TEXT NOT NULL DEFAULT '',
 native_id TEXT NOT NULL DEFAULT '',
 detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_network_ipam_reservations_pool ON network_ipam_reservations(pool_id,state);
`
