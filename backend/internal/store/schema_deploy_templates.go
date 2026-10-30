package store

// deployTemplateSchema holds the templates an operator adds to the one-click
// catalogue (deploy/user_templates.go): a definition in blueprint's own
// schema, validated as a built-in is, under an id in the user- namespace no
// built-in may use. Kept apart from `schema` so the block is read in one
// place; it is concatenated into `schema`, so the same rules hold: CREATE ...
// IF NOT EXISTS only, and a column this table gains after it has shipped goes
// into addedColumns.
const deployTemplateSchema = `
-- slug is the definition's own id (user-<name>), unique because the picker
-- and every source that deploys it name it by that. source is how it was
-- made: from a pasted Compose file, from a deployment's frozen plan, or
-- imported as JSON. from_project_id is the deployment a template was saved
-- from, kept as a plain number since the deployment may be removed while the
-- template stays. A deployment does not read this table to run: the
-- definition is frozen into its source when the source is saved.
CREATE TABLE IF NOT EXISTS deploy_templates (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  slug            TEXT NOT NULL UNIQUE,
  name            TEXT NOT NULL,
  version         TEXT NOT NULL,
  source          TEXT NOT NULL,
  definition_json TEXT NOT NULL,
  from_project_id INTEGER NOT NULL DEFAULT 0,
  created_by      TEXT NOT NULL DEFAULT '',
  updated_by      TEXT NOT NULL DEFAULT '',
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);
`
