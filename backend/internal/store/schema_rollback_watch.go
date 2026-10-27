package store

// rollbackWatchSchema holds metric-gated rollback (deploy/rollback_watch.go):
// an environment's terms for keeping the previous release warm after a
// blue/green cutover, and one row per window in which a new release was
// judged against the release it replaced. Kept apart from `schema` so the
// block is read in one place; it is concatenated into `schema`, so the same
// rules hold: CREATE ... IF NOT EXISTS only, and a column one of these tables
// gains after it has shipped goes into addedColumns.
const rollbackWatchSchema = `
-- No row is the defaults (deploy.DefaultRollbackPolicy): kept warm for ten
-- minutes and rolled back automatically. A row exists once somebody saved the
-- environment's terms; it goes with its environment.
CREATE TABLE IF NOT EXISTS deploy_rollback_policies (
  environment_id    INTEGER PRIMARY KEY REFERENCES deploy_environments(id) ON DELETE CASCADE,
  keep_warm         INTEGER NOT NULL DEFAULT 1,
  window_seconds    INTEGER NOT NULL DEFAULT 600,
  automatic         INTEGER NOT NULL DEFAULT 1,
  error_rate_margin REAL NOT NULL DEFAULT 5,
  latency_factor    REAL NOT NULL DEFAULT 2,
  min_requests      INTEGER NOT NULL DEFAULT 20,
  updated_by        TEXT NOT NULL DEFAULT '',
  updated_at        INTEGER NOT NULL
);
-- One window: release_id went live at cutover_at and warm_release_id, the
-- release it replaced, was paused rather than removed until expires_at. The
-- terms it is judged on are copied into policy_json when it opens, so an edit
-- made during the window applies to the next one. baseline_json is the warm
-- release's own traffic before the cutover and observed_json the new one's
-- since; verdict_json is what the last reading decided. rollback_run_id is
-- the instant rollback that was asked for, and the run that carries it out
-- moves the row to rolling_back. A closed row says why in end_reason.
CREATE TABLE IF NOT EXISTS deploy_rollback_watches (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id      INTEGER NOT NULL REFERENCES deploy_projects(id) ON DELETE CASCADE,
  environment_id  INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  run_id          INTEGER NOT NULL DEFAULT 0,
  release_id      INTEGER NOT NULL,
  warm_release_id INTEGER NOT NULL,
  warm_runtime_id TEXT NOT NULL DEFAULT '',
  state           TEXT NOT NULL,
  policy_json     TEXT NOT NULL DEFAULT '{}',
  cutover_at      INTEGER NOT NULL,
  expires_at      INTEGER NOT NULL,
  baseline_json   TEXT NOT NULL DEFAULT 'null',
  observed_json   TEXT NOT NULL DEFAULT 'null',
  verdict_json    TEXT NOT NULL DEFAULT 'null',
  checked_at      INTEGER NOT NULL DEFAULT 0,
  rollback_run_id INTEGER NOT NULL DEFAULT 0,
  attempts        INTEGER NOT NULL DEFAULT 0,
  end_reason      TEXT NOT NULL DEFAULT '',
  end_message     TEXT NOT NULL DEFAULT '',
  ended_at        INTEGER NOT NULL DEFAULT 0,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_deploy_rollback_watch_environment
  ON deploy_rollback_watches(environment_id, id DESC);
-- At most one open window per environment, so at most one release is ever
-- kept warm beside the live one.
CREATE UNIQUE INDEX IF NOT EXISTS idx_deploy_rollback_watch_open
  ON deploy_rollback_watches(environment_id) WHERE state IN ('watching', 'rolling_back', 'closing');
`
