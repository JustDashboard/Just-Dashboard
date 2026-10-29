package store

// previewBranchSchema holds database branches for pull request previews
// (deploy/preview_branches.go): what each database an environment links
// gives its previews, and the branches restored for them. Concatenated into
// `schema` like the others: CREATE ... IF NOT EXISTS only, and a column these
// tables gain after they have shipped goes into addedColumns.
const previewBranchSchema = `
-- One row per environment and linked database that gives its previews
-- something; no row is "nothing", the default. masked_columns is a JSON list
-- of table.column (or schema.table.column) names.
CREATE TABLE IF NOT EXISTS deploy_preview_branch_policies (
  environment_id  INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  connection_id   INTEGER NOT NULL,
  mode            TEXT NOT NULL DEFAULT 'none',
  masked_columns  TEXT NOT NULL DEFAULT '[]',
  updated_by      TEXT NOT NULL DEFAULT '',
  updated_at      INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (environment_id, connection_id)
);

-- A preview's own copy of a linked database, restored from its newest
-- backup into a container on the preview's database network. The container
-- carries the rehearsal label with "branch-<id>", which is how every sweep
-- finds it. secret_enc is sealed: the branch's generated sign-in, which a
-- refresh carries over so the preview's address stays the same.
CREATE TABLE IF NOT EXISTS deploy_preview_branches (
  id                     INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id             INTEGER NOT NULL,
  environment_id         INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  source_environment_id  INTEGER NOT NULL,
  connection_id          INTEGER NOT NULL,
  name                   TEXT NOT NULL DEFAULT '',
  engine                 TEXT NOT NULL DEFAULT '',
  database_name          TEXT NOT NULL DEFAULT '',
  mode                   TEXT NOT NULL,
  masked_columns         TEXT NOT NULL DEFAULT '[]',
  state                  TEXT NOT NULL,
  source_kind            TEXT NOT NULL DEFAULT '',
  source_at              INTEGER NOT NULL DEFAULT 0,
  backup_run_id          INTEGER NOT NULL DEFAULT 0,
  recovered_to           INTEGER NOT NULL DEFAULT 0,
  container              TEXT NOT NULL DEFAULT '',
  network                TEXT NOT NULL DEFAULT '',
  host                   TEXT NOT NULL DEFAULT '',
  port                   INTEGER NOT NULL DEFAULT 0,
  secret_enc             TEXT NOT NULL DEFAULT '',
  masked_rows            INTEGER NOT NULL DEFAULT 0,
  reason                 TEXT NOT NULL DEFAULT '',
  restore_ms             INTEGER NOT NULL DEFAULT 0,
  requested_by           TEXT NOT NULL DEFAULT '',
  created_at             INTEGER NOT NULL,
  ready_at               INTEGER NOT NULL DEFAULT 0,
  ended_at               INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_deploy_preview_branches_environment ON deploy_preview_branches(environment_id, connection_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_deploy_preview_branches_state ON deploy_preview_branches(state);
`
