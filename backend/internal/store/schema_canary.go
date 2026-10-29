package store

// canarySchema holds canary releases (deploy/canary.go): one row per trial in
// which a candidate release took a share of the live route's requests beside
// the live release. Kept apart from `schema` so the block is read in one
// place; it is concatenated into `schema`, so the same rules hold: CREATE ...
// IF NOT EXISTS only, and a column this table gains after it has shipped goes
// into addedColumns.
const canarySchema = `
-- One trial: release_id, the candidate, took percent of the requests of the
-- route live_release_id serves from started_at until ends_at, and is judged
-- against it on the terms copied into policy_json when it opened.
-- live_json and candidate_json are the two releases' readings over the
-- window so far, verdict_json what the last reading decided. decision is an
-- operator's promote or abort, asked for and carried out by the run that
-- holds the trial (run_id). A closed row says how in end_reason.
CREATE TABLE IF NOT EXISTS deploy_canaries (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id       INTEGER NOT NULL REFERENCES deploy_projects(id) ON DELETE CASCADE,
  environment_id   INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  run_id           INTEGER NOT NULL,
  release_id       INTEGER NOT NULL,
  live_release_id  INTEGER NOT NULL,
  percent          INTEGER NOT NULL,
  duration_seconds INTEGER NOT NULL,
  auto_promote     INTEGER NOT NULL DEFAULT 0,
  policy_json      TEXT NOT NULL DEFAULT '{}',
  state            TEXT NOT NULL,
  decision         TEXT NOT NULL DEFAULT '',
  decided_by       TEXT NOT NULL DEFAULT '',
  started_at       INTEGER NOT NULL,
  ends_at          INTEGER NOT NULL,
  live_json        TEXT NOT NULL DEFAULT 'null',
  candidate_json   TEXT NOT NULL DEFAULT 'null',
  verdict_json     TEXT NOT NULL DEFAULT 'null',
  checked_at       INTEGER NOT NULL DEFAULT 0,
  end_reason       TEXT NOT NULL DEFAULT '',
  end_message      TEXT NOT NULL DEFAULT '',
  ended_at         INTEGER NOT NULL DEFAULT 0,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_deploy_canary_environment
  ON deploy_canaries(environment_id, id DESC);
-- At most one trial at a time per environment: its runs are serialized, and
-- the route has room for one candidate.
CREATE UNIQUE INDEX IF NOT EXISTS idx_deploy_canary_open
  ON deploy_canaries(environment_id) WHERE state IN ('running', 'holding');
`
