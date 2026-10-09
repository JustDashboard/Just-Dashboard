package store

// Egress groups keep their decisions and measurements here; the groups
// themselves are host configuration in the network spec. Events are every
// decision the monitor or an operator made — why, the evidence measured and
// the member set before and after — and the state changes behind them.
// Samples are each member's bounded probe history; simulations are the
// namespace runs automation is gated on.
const networkEgressSchema = `
CREATE TABLE IF NOT EXISTS network_egress_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 group_id INTEGER NOT NULL,
 at INTEGER NOT NULL,
 kind TEXT NOT NULL,
 action TEXT NOT NULL DEFAULT '',
 member_id INTEGER NOT NULL DEFAULT 0,
 outcome TEXT NOT NULL DEFAULT '',
 reason TEXT NOT NULL DEFAULT '',
 actor TEXT NOT NULL DEFAULT '',
 before_members TEXT NOT NULL DEFAULT '',
 after_members TEXT NOT NULL DEFAULT '',
 evidence TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_network_egress_events_group ON network_egress_events(group_id, at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_network_egress_events_outcome ON network_egress_events(outcome);
CREATE TABLE IF NOT EXISTS network_egress_samples (
 group_id INTEGER NOT NULL,
 member_id INTEGER NOT NULL,
 at INTEGER NOT NULL,
 ok INTEGER NOT NULL DEFAULT 0,
 total INTEGER NOT NULL DEFAULT 0,
 loss REAL NOT NULL DEFAULT 0,
 latency_ms REAL NOT NULL DEFAULT 0,
 good INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL DEFAULT '',
 detail TEXT NOT NULL DEFAULT '',
 PRIMARY KEY (group_id, member_id, at)
);
CREATE INDEX IF NOT EXISTS idx_network_egress_samples_at ON network_egress_samples(at);
CREATE TABLE IF NOT EXISTS network_egress_simulations (
 id TEXT PRIMARY KEY,
 group_id INTEGER NOT NULL,
 fingerprint TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT '',
 actor TEXT NOT NULL DEFAULT '',
 started_at INTEGER NOT NULL DEFAULT 0,
 finished_at INTEGER NOT NULL DEFAULT 0,
 result TEXT NOT NULL DEFAULT '',
 error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_network_egress_simulations_group ON network_egress_simulations(group_id, started_at DESC);
`
