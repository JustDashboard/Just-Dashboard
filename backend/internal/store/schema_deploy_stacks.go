package store

// deployStackSchema holds the smart stacks (internal/stacks): a deployment
// made from a stack's preset, the configuration steps the dashboard ran
// against its live release, and the secrets it created for the applications
// (administrator passwords, API keys an application handed back), sealed.
// Concatenated into `schema`: CREATE ... IF NOT EXISTS only, and a column
// these tables gain after they have shipped goes into addedColumns.
//
// Every row belongs to one project and goes with it: a stack's secrets are
// worthless once its containers are gone, and keeping them would keep
// passwords for nothing.
const deployStackSchema = `
-- One row per stack deployment. live_release_id is the release the last pass
-- was asked for; configured_release_id and configured_version the release and
-- manifest version a pass last finished for, so a new release or a changed
-- manifest starts a pass. requested_at is a Retry pressed since.
CREATE TABLE IF NOT EXISTS deploy_stacks (
  project_id            INTEGER PRIMARY KEY REFERENCES deploy_projects(id) ON DELETE CASCADE,
  environment_id        INTEGER NOT NULL,
  stack_id              TEXT NOT NULL,
  preset                TEXT NOT NULL,
  status                TEXT NOT NULL DEFAULT 'waiting',
  live_release_id       INTEGER NOT NULL DEFAULT 0,
  configured_release_id INTEGER NOT NULL DEFAULT 0,
  configured_version    TEXT NOT NULL DEFAULT '',
  requested_at          INTEGER NOT NULL DEFAULT 0,
  started_at            INTEGER NOT NULL DEFAULT 0,
  finished_at           INTEGER NOT NULL DEFAULT 0,
  created_by            TEXT NOT NULL DEFAULT '',
  created_at            INTEGER NOT NULL,
  updated_at            INTEGER NOT NULL
);
-- The last outcome of each step. outputs_json holds only the non-secret facts
-- a step reports; error is redacted before it is written.
CREATE TABLE IF NOT EXISTS deploy_stack_steps (
  project_id   INTEGER NOT NULL REFERENCES deploy_stacks(project_id) ON DELETE CASCADE,
  step_id      TEXT NOT NULL,
  status       TEXT NOT NULL,
  attempts     INTEGER NOT NULL DEFAULT 0,
  message      TEXT NOT NULL DEFAULT '',
  error        TEXT NOT NULL DEFAULT '',
  outputs_json TEXT NOT NULL DEFAULT '{}',
  action_json  TEXT NOT NULL DEFAULT '',
  started_at   INTEGER NOT NULL DEFAULT 0,
  finished_at  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (project_id, step_id)
);
-- sealed is auth.Sealer's envelope; nothing here is ever returned except
-- through the audited reveal route.
CREATE TABLE IF NOT EXISTS deploy_stack_credentials (
  project_id INTEGER NOT NULL REFERENCES deploy_stacks(project_id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  sealed     TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (project_id, name)
);
`
