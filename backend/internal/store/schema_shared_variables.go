package store

// sharedVariableSchema holds the deployments' shared variables
// (deploy/shared_variables.go): a value defined once and read by any
// environment whose own variable references it. Kept apart from `schema` so
// the block is read in one place; it is concatenated into `schema`, so the
// same rules hold: CREATE ... IF NOT EXISTS only, and a column this table
// gains after it has shipped goes into addedColumns.
const sharedVariableSchema = `
-- project_id NULL is the server's own, read as ${{server.NAME}} by every
-- project; a project's is read as ${{project.NAME}} by that project's
-- environments alone, and goes with the project. The value is sealed like a
-- deployment variable's. There are no revisions: a reference resolves the
-- current value when a run starts, the way a credential does, and the
-- release keeps the reference rather than the value.
CREATE TABLE IF NOT EXISTS deploy_shared_variables (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id   INTEGER REFERENCES deploy_projects(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  sensitivity  TEXT NOT NULL DEFAULT 'secret',
  value_enc    TEXT NOT NULL,
  value_digest TEXT NOT NULL DEFAULT '',
  updated_by   TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);
-- One name per scope. A NULL project_id would never collide in a plain
-- UNIQUE, so the server's are folded onto 0, which no project id is.
CREATE UNIQUE INDEX IF NOT EXISTS idx_deploy_shared_variable_name
  ON deploy_shared_variables(IFNULL(project_id, 0), name);
`
