package store

// dataRollbackSchema holds data-aware rollback (deploy/data_rollback.go): the
// point each linked database could be brought back to when a release went
// live, and the throwaway copies restored from those points. Kept apart from
// `schema` so the block is read in one place; it is concatenated into
// `schema`, so the same rules hold: CREATE ... IF NOT EXISTS only, and a
// column these tables gain after they have shipped goes into addedColumns.
const dataRollbackSchema = `
-- One row per linked database, written once when the release first went
-- live: a moment its point-in-time archive recovers to, the backup run whose
-- dump covers it, or none and why. A release activated before this existed
-- has no row, and says so.
CREATE TABLE IF NOT EXISTS deploy_release_data_points (
  release_id     INTEGER NOT NULL REFERENCES deploy_releases(id) ON DELETE CASCADE,
  connection_id  INTEGER NOT NULL,
  name           TEXT NOT NULL DEFAULT '',
  engine         TEXT NOT NULL DEFAULT '',
  kind           TEXT NOT NULL,
  point_at       INTEGER NOT NULL DEFAULT 0,
  backup_run_id  INTEGER NOT NULL DEFAULT 0,
  backup_job_id  INTEGER NOT NULL DEFAULT 0,
  archive_id     INTEGER NOT NULL DEFAULT 0,
  reason         TEXT NOT NULL DEFAULT '',
  recorded_at    INTEGER NOT NULL,
  PRIMARY KEY (release_id, connection_id)
);

-- A database restored as of a release into a side container on the
-- deployment's network. The row outlives the container by a day so the run
-- page and the list can say what became of it; the container carries the
-- rehearsal label with "copy-<id>", which is how every sweep finds it.
-- secret_enc is sealed: the copy's generated sign-in.
CREATE TABLE IF NOT EXISTS deploy_data_copies (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id      INTEGER NOT NULL,
  environment_id  INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  release_id      INTEGER NOT NULL REFERENCES deploy_releases(id) ON DELETE CASCADE,
  release_number  INTEGER NOT NULL DEFAULT 0,
  connection_id   INTEGER NOT NULL,
  name            TEXT NOT NULL DEFAULT '',
  engine          TEXT NOT NULL DEFAULT '',
  database_name   TEXT NOT NULL DEFAULT '',
  state           TEXT NOT NULL,
  source_kind     TEXT NOT NULL,
  source_at       INTEGER NOT NULL DEFAULT 0,
  backup_run_id   INTEGER NOT NULL DEFAULT 0,
  backup_job_id   INTEGER NOT NULL DEFAULT 0,
  archive_id      INTEGER NOT NULL DEFAULT 0,
  recovered_to    INTEGER NOT NULL DEFAULT 0,
  after_run_id    INTEGER NOT NULL DEFAULT 0,
  container       TEXT NOT NULL DEFAULT '',
  network         TEXT NOT NULL DEFAULT '',
  host            TEXT NOT NULL DEFAULT '',
  port            INTEGER NOT NULL DEFAULT 0,
  secret_enc      TEXT NOT NULL DEFAULT '',
  expires_at      INTEGER NOT NULL DEFAULT 0,
  reason          TEXT NOT NULL DEFAULT '',
  restore_ms      INTEGER NOT NULL DEFAULT 0,
  created_by      TEXT NOT NULL DEFAULT '',
  created_at      INTEGER NOT NULL,
  ready_at        INTEGER NOT NULL DEFAULT 0,
  ended_at        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_deploy_data_copies_environment ON deploy_data_copies(environment_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_deploy_data_copies_state ON deploy_data_copies(state);
`
