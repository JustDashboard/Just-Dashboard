package store

// rehearsalSchema holds migration rehearsal's terms (deploy/rehearsal.go):
// whether an environment's release tasks are first run against a throwaway
// copy of its database, which database, and what a rehearsal that cannot run
// does to the release. Kept apart from `schema` so the block is read in one
// place; it is concatenated into `schema`, so the same rules hold: CREATE ...
// IF NOT EXISTS only, and a column this table gains after it has shipped
// goes into addedColumns.
const rehearsalSchema = `
-- No row is the defaults (deploy.DefaultRehearsalPolicy): off. A row exists
-- once somebody saved the environment's terms, and goes with it.
CREATE TABLE IF NOT EXISTS deploy_rehearsal_policies (
  environment_id          INTEGER PRIMARY KEY REFERENCES deploy_environments(id) ON DELETE CASCADE,
  enabled                 INTEGER NOT NULL DEFAULT 0,
  connection_id           INTEGER NOT NULL DEFAULT 0,
  when_unavailable        TEXT NOT NULL DEFAULT 'block',
  restore_timeout_seconds INTEGER NOT NULL DEFAULT 600,
  max_backup_mb           INTEGER NOT NULL DEFAULT 2048,
  updated_by              TEXT NOT NULL DEFAULT '',
  updated_at              INTEGER NOT NULL
);
`
