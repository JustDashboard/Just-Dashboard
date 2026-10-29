package store

// deployEventSchema holds what Docker did to a deployment's containers
// (dockerx/events_store.go), kept past the process that heard it. Kept apart
// from `schema` so the block is read in one place; it is concatenated into
// `schema`, so the same rules hold: CREATE ... IF NOT EXISTS only, and a
// column one of these tables gains after it has shipped goes into addedColumns.
const deployEventSchema = `
-- One row per Docker event about something a deployment owns: its containers
-- (recognised by the dashboard's own labels on the event) and its networks
-- (recognised by name, since the daemon sends a network's labels on no event).
-- The daemon keeps none of these and the in-memory ring that used to be the
-- only record emptied on every restart of the dashboard, so an OOM kill at
-- four in the morning was gone by the time anyone asked about it.
--
-- ts_nano is the daemon's own timestamp to the nanosecond, so a row and the
-- same event still in the ring are one event to the feed that reads both. The
-- typed columns are what the counters and the availability figure read; the
-- event as the feed renders it is kept whole in event_json so a row is drawn
-- exactly as it was the moment it arrived. Rows are pruned past the events'
-- retention, which is at least thirty days whatever JD_METRICS_RETENTION says.
CREATE TABLE IF NOT EXISTS deploy_container_events (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  ts_nano        INTEGER NOT NULL,
  environment_id INTEGER NOT NULL,
  release_id     INTEGER NOT NULL DEFAULT 0,
  release_number INTEGER NOT NULL DEFAULT 0,
  run_id         INTEGER NOT NULL DEFAULT 0,
  kind           TEXT NOT NULL,
  action         TEXT NOT NULL,
  name           TEXT NOT NULL DEFAULT '',
  container_id   TEXT NOT NULL DEFAULT '',
  exit_code      TEXT NOT NULL DEFAULT '',
  signal         TEXT NOT NULL DEFAULT '',
  -- A kill that followed a stop asked of the same container, as against a
  -- signal nobody here sent.
  requested      INTEGER NOT NULL DEFAULT 0,
  -- The cause as the feed names it: daemon, compose or docker.
  source         TEXT NOT NULL DEFAULT '',
  event_json     TEXT NOT NULL DEFAULT '{}'
);
-- The feed and the availability figure read one environment over a window;
-- pruning walks by time across every environment.
CREATE INDEX IF NOT EXISTS idx_deploy_container_events_environment
  ON deploy_container_events(environment_id, ts_nano);
CREATE INDEX IF NOT EXISTS idx_deploy_container_events_ts
  ON deploy_container_events(ts_nano);
`
