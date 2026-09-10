package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store struct {
	DB *sql.DB
}

// DatabaseFile is the SQLite file inside the data directory. It keeps its
// original name through the "Just Dashboard" rename: moving it would buy
// nothing and would strand every existing install's accounts, audit log and
// encrypted secrets. config.Load looks for it by this name when deciding
// whether a pre-rename data directory should be adopted.
const DatabaseFile = "vpsd.db"

const schema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS users (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  username       TEXT NOT NULL UNIQUE,
  password_hash  TEXT NOT NULL,
  role           TEXT NOT NULL,
  totp_secret    TEXT NOT NULL DEFAULT '',
  totp_enabled   INTEGER NOT NULL DEFAULT 0,
  disabled       INTEGER NOT NULL DEFAULT 0,
  must_change_pw INTEGER NOT NULL DEFAULT 0,
  failed_count   INTEGER NOT NULL DEFAULT 0,
  locked_until   INTEGER NOT NULL DEFAULT 0,
  last_login_at  INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS recovery_codes (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_hash  TEXT NOT NULL,
  used_at    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS sessions (
  id           TEXT PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash   TEXT NOT NULL UNIQUE,
  twofa_passed INTEGER NOT NULL DEFAULT 0,
  ip           TEXT NOT NULL DEFAULT '',
  user_agent   TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);

CREATE TABLE IF NOT EXISTS api_tokens (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  prefix       TEXT NOT NULL,
  token_hash   TEXT NOT NULL UNIQUE,
  role         TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL DEFAULT 0,
  last_used_at INTEGER NOT NULL DEFAULT 0,
  revoked      INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS audit_log (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  ts        INTEGER NOT NULL,
  user_id   INTEGER NOT NULL DEFAULT 0,
  username  TEXT NOT NULL DEFAULT '',
  role      TEXT NOT NULL DEFAULT '',
  ip        TEXT NOT NULL DEFAULT '',
  actor     TEXT NOT NULL DEFAULT 'session',
  action    TEXT NOT NULL,
  target    TEXT NOT NULL DEFAULT '',
  method    TEXT NOT NULL DEFAULT '',
  path      TEXT NOT NULL DEFAULT '',
  status    INTEGER NOT NULL DEFAULT 0,
  success   INTEGER NOT NULL DEFAULT 1,
  detail    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_log(ts DESC);
CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_log(user_id);

CREATE TABLE IF NOT EXISTS db_connections (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL UNIQUE,
  driver     TEXT NOT NULL,
  dsn_enc    TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

-- Named SQL snippets an operator keeps against a connection. The SQL is stored
-- in the clear: it is a query the operator wrote, not a credential, and the
-- connection it runs against carries the secret. A NULL connection_id would be
-- a snippet shared across connections, but membership is kept concrete for now.
CREATE TABLE IF NOT EXISTS db_saved_queries (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  connection_id INTEGER NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  sql           TEXT NOT NULL,
  created_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_db_saved_conn ON db_saved_queries(connection_id, name);

-- Recent statements per connection, so the Query tab can offer history without
-- re-reading the audit log (which mixes every action and is admin-only). It is
-- pruned to the most recent rows per connection on write, so it never grows
-- without bound.
CREATE TABLE IF NOT EXISTS db_query_history (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  connection_id INTEGER NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
  sql           TEXT NOT NULL,
  risk          TEXT NOT NULL DEFAULT 'read',
  success       INTEGER NOT NULL DEFAULT 1,
  duration_ms   INTEGER NOT NULL DEFAULT 0,
  row_count     INTEGER NOT NULL DEFAULT 0,
  ran_at        INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_db_history_conn ON db_query_history(connection_id, ran_at DESC);

CREATE TABLE IF NOT EXISTS backup_jobs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT NOT NULL UNIQUE,
  sources     TEXT NOT NULL,
  excludes    TEXT NOT NULL DEFAULT '',
  target_kind TEXT NOT NULL,
  target_cfg  TEXT NOT NULL DEFAULT '{}',
  secrets_enc TEXT NOT NULL DEFAULT '',
  schedule    TEXT NOT NULL DEFAULT '',
  retention   INTEGER NOT NULL DEFAULT 7,
  enabled     INTEGER NOT NULL DEFAULT 1,
  created_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS backup_runs (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id     INTEGER NOT NULL REFERENCES backup_jobs(id) ON DELETE CASCADE,
  started_at INTEGER NOT NULL,
  ended_at   INTEGER NOT NULL DEFAULT 0,
  status     TEXT NOT NULL,
  artifact   TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  log        TEXT NOT NULL DEFAULT '',
  trigger    TEXT NOT NULL DEFAULT 'manual'
);
CREATE INDEX IF NOT EXISTS idx_backup_runs_job ON backup_runs(job_id, started_at DESC);

CREATE TABLE IF NOT EXISTS deploy_projects (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  name          TEXT NOT NULL UNIQUE,
  profile       TEXT NOT NULL DEFAULT 'compose',
  repo_path     TEXT NOT NULL,
  branch        TEXT NOT NULL DEFAULT 'main',
  compose_file  TEXT NOT NULL DEFAULT 'docker-compose.yml',
  pre_command   TEXT NOT NULL DEFAULT '',
  post_command  TEXT NOT NULL DEFAULT '',
  hook_secret   TEXT NOT NULL,
  hook_id       TEXT NOT NULL UNIQUE,
  enabled       INTEGER NOT NULL DEFAULT 1,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL DEFAULT 0,
  archived_at   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS deploy_env (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL REFERENCES deploy_projects(id) ON DELETE CASCADE,
  key        TEXT NOT NULL,
  value_enc  TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE(project_id, key)
);

CREATE TABLE IF NOT EXISTS deploy_runs (
  id                   INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id           INTEGER NOT NULL REFERENCES deploy_projects(id) ON DELETE CASCADE,
  environment_id       INTEGER NOT NULL DEFAULT 0,
  started_at           INTEGER NOT NULL,
  ended_at             INTEGER NOT NULL DEFAULT 0,
  status               TEXT NOT NULL,
  state                TEXT NOT NULL DEFAULT '',
  operation            TEXT NOT NULL DEFAULT 'deploy',
  trigger              TEXT NOT NULL DEFAULT 'manual',
  actor                TEXT NOT NULL DEFAULT '',
  from_commit          TEXT NOT NULL DEFAULT '',
  to_commit            TEXT NOT NULL DEFAULT '',
  log                  TEXT NOT NULL DEFAULT '',
  requested_at         INTEGER NOT NULL DEFAULT 0,
  queued_at            INTEGER NOT NULL DEFAULT 0,
  claimed_at           INTEGER NOT NULL DEFAULT 0,
  heartbeat_at         INTEGER NOT NULL DEFAULT 0,
  cancel_requested     INTEGER NOT NULL DEFAULT 0,
  superseded_by        INTEGER NOT NULL DEFAULT 0,
  retry_of_run_id      INTEGER NOT NULL DEFAULT 0,
  idempotency_key      TEXT NOT NULL DEFAULT '',
  request_digest       TEXT NOT NULL DEFAULT '',
  plan_revision        INTEGER NOT NULL DEFAULT 0,
  release_id           INTEGER NOT NULL DEFAULT 0,
  candidate_release_id INTEGER NOT NULL DEFAULT 0,
  terminal_code        TEXT NOT NULL DEFAULT '',
  terminal_reason      TEXT NOT NULL DEFAULT '',
  lease_token          TEXT NOT NULL DEFAULT '',
  lease_until          INTEGER NOT NULL DEFAULT 0,
  priority             INTEGER NOT NULL DEFAULT 100,
  slot_class           TEXT NOT NULL DEFAULT 'light',
  metadata_json        TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_deploy_runs_project ON deploy_runs(project_id, started_at DESC);

-- The shipped project row remains the stable deployment identity. Environments
-- carry mutable desired configuration and point at an immutable live release.
CREATE TABLE IF NOT EXISTS deploy_environments (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id        INTEGER NOT NULL REFERENCES deploy_projects(id) ON DELETE CASCADE,
  name              TEXT NOT NULL,
  slug              TEXT NOT NULL,
  kind              TEXT NOT NULL DEFAULT 'production',
  desired_revision  INTEGER NOT NULL DEFAULT 1,
  live_release_id   INTEGER NOT NULL DEFAULT 0,
  strategy          TEXT NOT NULL DEFAULT 'stop_first',
  expected_downtime INTEGER NOT NULL DEFAULT 1,
  protected         INTEGER NOT NULL DEFAULT 1,
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL,
  archived_at       INTEGER NOT NULL DEFAULT 0,
  UNIQUE(project_id, slug)
);
CREATE INDEX IF NOT EXISTS idx_deploy_env_project ON deploy_environments(project_id, archived_at, id);

CREATE TABLE IF NOT EXISTS deploy_credentials (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT NOT NULL UNIQUE,
  kind        TEXT NOT NULL,
  config_json TEXT NOT NULL DEFAULT '{}',
  secret_enc  TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS deploy_sources (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  environment_id INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  revision       INTEGER NOT NULL,
  kind           TEXT NOT NULL,
  config_json    TEXT NOT NULL DEFAULT '{}',
  credential_id  INTEGER NOT NULL DEFAULT 0,
  identity_json  TEXT NOT NULL DEFAULT '{}',
  digest         TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  UNIQUE(environment_id, revision)
);

CREATE TABLE IF NOT EXISTS deploy_build_plans (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  environment_id INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  revision       INTEGER NOT NULL,
  method         TEXT NOT NULL,
  config_json    TEXT NOT NULL DEFAULT '{}',
  evidence_json  TEXT NOT NULL DEFAULT '[]',
  preview        TEXT NOT NULL DEFAULT '',
  digest         TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  UNIQUE(environment_id, revision)
);

CREATE TABLE IF NOT EXISTS deploy_runtime_plans (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  environment_id INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  revision       INTEGER NOT NULL,
  config_json    TEXT NOT NULL DEFAULT '{}',
  preview        TEXT NOT NULL DEFAULT '',
  digest         TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  UNIQUE(environment_id, revision)
);

CREATE TABLE IF NOT EXISTS deploy_releases (
  id                     INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id             INTEGER NOT NULL REFERENCES deploy_projects(id) ON DELETE CASCADE,
  environment_id         INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  release_number         INTEGER NOT NULL,
  run_id                 INTEGER NOT NULL DEFAULT 0,
  predecessor_release_id INTEGER NOT NULL DEFAULT 0,
  state                  TEXT NOT NULL DEFAULT 'retained',
  plan_revision          INTEGER NOT NULL DEFAULT 1,
  source_id              INTEGER NOT NULL DEFAULT 0,
  build_plan_id          INTEGER NOT NULL DEFAULT 0,
  runtime_plan_id        INTEGER NOT NULL DEFAULT 0,
  source_revision        TEXT NOT NULL DEFAULT '',
  source_identity_json   TEXT NOT NULL DEFAULT '{}',
  image_digest           TEXT NOT NULL DEFAULT '',
  config_digest          TEXT NOT NULL DEFAULT '',
  variables_digest       TEXT NOT NULL DEFAULT '',
  strategy               TEXT NOT NULL DEFAULT 'stop_first',
  expected_downtime      INTEGER NOT NULL DEFAULT 1,
  provenance_json        TEXT NOT NULL DEFAULT '{}',
  blueprint_id           TEXT NOT NULL DEFAULT '',
  blueprint_version      TEXT NOT NULL DEFAULT '',
  created_at             INTEGER NOT NULL,
  activated_at           INTEGER NOT NULL DEFAULT 0,
  retired_at             INTEGER NOT NULL DEFAULT 0,
  pinned                 INTEGER NOT NULL DEFAULT 0,
  UNIQUE(environment_id, release_number)
);
CREATE INDEX IF NOT EXISTS idx_deploy_release_environment
  ON deploy_releases(environment_id, release_number DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_deploy_release_legacy_run
  ON deploy_releases(run_id) WHERE run_id <> 0;

CREATE TABLE IF NOT EXISTS deploy_release_artifacts (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  release_id    INTEGER NOT NULL REFERENCES deploy_releases(id) ON DELETE CASCADE,
  kind          TEXT NOT NULL,
  reference     TEXT NOT NULL DEFAULT '',
  digest        TEXT NOT NULL DEFAULT '',
  metadata_json TEXT NOT NULL DEFAULT '{}',
  size_bytes    INTEGER NOT NULL DEFAULT 0,
  retain_until  INTEGER NOT NULL DEFAULT 0,
  state         TEXT NOT NULL DEFAULT 'available',
  created_at    INTEGER NOT NULL,
  UNIQUE(release_id, kind, digest, reference)
);

-- Release identity and provenance are append-only. Lifecycle pointers may
-- change state/timestamps/pins, but a release can never be rewritten to name a
-- different source, plan, artifact digest, actor or predecessor.
CREATE TRIGGER IF NOT EXISTS deploy_release_snapshot_immutable
BEFORE UPDATE OF project_id, environment_id, release_number, run_id,
  predecessor_release_id, plan_revision, source_id, build_plan_id,
  runtime_plan_id, source_revision, source_identity_json, image_digest,
  config_digest, variables_digest, strategy, provenance_json, blueprint_id,
  blueprint_version, created_at ON deploy_releases
BEGIN
  SELECT RAISE(ABORT, 'deployment release snapshot is immutable');
END;

-- Cleanup changes only retention/lifecycle state. Artifact identity and its
-- secret-free provenance stay available as permanent release evidence.
CREATE TRIGGER IF NOT EXISTS deploy_release_artifact_identity_immutable
BEFORE UPDATE OF release_id, kind, reference, digest, metadata_json, size_bytes,
  created_at ON deploy_release_artifacts
BEGIN
  SELECT RAISE(ABORT, 'deployment release artifact identity is immutable');
END;

-- Runtime identity is mutable only in lifecycle state. The container id or
-- Compose project/root attached to an immutable release cannot be rewritten
-- after recovery starts using it as owning-feature evidence.
CREATE TABLE IF NOT EXISTS deploy_release_runtimes (
  release_id        INTEGER PRIMARY KEY REFERENCES deploy_releases(id) ON DELETE CASCADE,
  environment_id    INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  kind              TEXT NOT NULL,
  runtime_id        TEXT NOT NULL,
  name              TEXT NOT NULL DEFAULT '',
  working_directory TEXT NOT NULL DEFAULT '',
  host              TEXT NOT NULL DEFAULT '',
  port              INTEGER NOT NULL DEFAULT 0,
  state             TEXT NOT NULL DEFAULT 'candidate',
  metadata_json     TEXT NOT NULL DEFAULT '{}',
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_deploy_release_runtime_environment
  ON deploy_release_runtimes(environment_id, state, release_id);
CREATE TRIGGER IF NOT EXISTS deploy_release_runtime_identity_immutable
BEFORE UPDATE OF release_id, environment_id, kind, runtime_id, name,
  working_directory, host, port, metadata_json, created_at
  ON deploy_release_runtimes
BEGIN
  SELECT RAISE(ABORT, 'deployment release runtime identity is immutable');
END;

-- Versioned source/build/runtime rows are append-only inputs to releases.
-- Editing one in place would silently change an already queued run even if
-- the release row itself remained immutable.
CREATE TRIGGER IF NOT EXISTS deploy_source_revision_immutable
BEFORE UPDATE ON deploy_sources
BEGIN
  SELECT RAISE(ABORT, 'deployment source revision is immutable');
END;
CREATE TRIGGER IF NOT EXISTS deploy_build_plan_revision_immutable
BEFORE UPDATE ON deploy_build_plans
BEGIN
  SELECT RAISE(ABORT, 'deployment build plan revision is immutable');
END;
CREATE TRIGGER IF NOT EXISTS deploy_runtime_plan_revision_immutable
BEFORE UPDATE ON deploy_runtime_plans
BEGIN
  SELECT RAISE(ABORT, 'deployment runtime plan revision is immutable');
END;
CREATE TABLE IF NOT EXISTS deploy_steps (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id          INTEGER NOT NULL REFERENCES deploy_runs(id) ON DELETE CASCADE,
  step_key        TEXT NOT NULL,
  ordinal         INTEGER NOT NULL,
  status          TEXT NOT NULL DEFAULT 'pending',
  attempt         INTEGER NOT NULL DEFAULT 1,
  timeout_seconds INTEGER NOT NULL DEFAULT 0,
  started_at      INTEGER NOT NULL DEFAULT 0,
  ended_at        INTEGER NOT NULL DEFAULT 0,
  evidence_json   TEXT NOT NULL DEFAULT '{}',
  error_code      TEXT NOT NULL DEFAULT '',
  error_message   TEXT NOT NULL DEFAULT '',
  cleanup_json    TEXT NOT NULL DEFAULT '{}',
  last_seq        INTEGER NOT NULL DEFAULT 0,
  UNIQUE(run_id, step_key, attempt)
);
CREATE INDEX IF NOT EXISTS idx_deploy_steps_run ON deploy_steps(run_id, ordinal, attempt);

-- All stream events share the per-run sequence. Log text is a specialised
-- payload rather than a second ordering mechanism, so reconnect cannot race a
-- state update against a line emitted beside it.
CREATE TABLE IF NOT EXISTS deploy_log_chunks (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id      INTEGER NOT NULL REFERENCES deploy_runs(id) ON DELETE CASCADE,
  step_id     INTEGER NOT NULL DEFAULT 0,
  seq         INTEGER NOT NULL,
  event_type  TEXT NOT NULL DEFAULT 'step.log',
  stream      TEXT NOT NULL DEFAULT 'status',
  ts          INTEGER NOT NULL,
  text        TEXT NOT NULL DEFAULT '',
  data_json   TEXT NOT NULL DEFAULT '{}',
  redacted    INTEGER NOT NULL DEFAULT 0,
  truncated   INTEGER NOT NULL DEFAULT 0,
  compacted   INTEGER NOT NULL DEFAULT 0,
  UNIQUE(run_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_deploy_log_run ON deploy_log_chunks(run_id, seq);

CREATE TABLE IF NOT EXISTS deploy_dependencies (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  environment_id INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  release_id     INTEGER NOT NULL DEFAULT 0,
  kind           TEXT NOT NULL,
  ownership      TEXT NOT NULL,
  resource_kind  TEXT NOT NULL,
  resource_id    TEXT NOT NULL DEFAULT '',
  config_json    TEXT NOT NULL DEFAULT '{}',
  created_at     INTEGER NOT NULL,
  UNIQUE(environment_id, release_id, kind, resource_kind, resource_id)
);

CREATE TABLE IF NOT EXISTS deploy_checks (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  environment_id  INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  runtime_plan_id INTEGER NOT NULL DEFAULT 0,
  name            TEXT NOT NULL,
  kind            TEXT NOT NULL,
  phase           TEXT NOT NULL DEFAULT 'readiness',
  config_json     TEXT NOT NULL DEFAULT '{}',
  required        INTEGER NOT NULL DEFAULT 1,
  ordinal         INTEGER NOT NULL DEFAULT 0,
  created_at      INTEGER NOT NULL,
  UNIQUE(environment_id, runtime_plan_id, name)
);

CREATE TABLE IF NOT EXISTS deploy_triggers (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  environment_id   INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  name             TEXT NOT NULL,
  kind             TEXT NOT NULL,
  provider         TEXT NOT NULL DEFAULT '',
  config_json      TEXT NOT NULL DEFAULT '{}',
  secret_enc       TEXT NOT NULL DEFAULT '',
  hook_id          TEXT NOT NULL DEFAULT '',
  enabled          INTEGER NOT NULL DEFAULT 1,
  last_delivery_at INTEGER NOT NULL DEFAULT 0,
  last_status      TEXT NOT NULL DEFAULT '',
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL,
  UNIQUE(environment_id, kind, name)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_deploy_trigger_hook
  ON deploy_triggers(hook_id) WHERE hook_id <> '';

CREATE TABLE IF NOT EXISTS deploy_webhook_deliveries (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  trigger_id  INTEGER NOT NULL REFERENCES deploy_triggers(id) ON DELETE CASCADE,
  delivery_id TEXT NOT NULL,
  event       TEXT NOT NULL DEFAULT '',
  repository  TEXT NOT NULL DEFAULT '',
  ref         TEXT NOT NULL DEFAULT '',
  body_digest TEXT NOT NULL,
  status      TEXT NOT NULL,
  reason      TEXT NOT NULL DEFAULT '',
  run_id      INTEGER NOT NULL DEFAULT 0,
  received_at INTEGER NOT NULL,
  UNIQUE(trigger_id, delivery_id)
);

CREATE TABLE IF NOT EXISTS deploy_variable_revisions (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  environment_id INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  key            TEXT NOT NULL,
  revision       INTEGER NOT NULL,
  sensitivity    TEXT NOT NULL DEFAULT 'secret',
  scopes         TEXT NOT NULL DEFAULT 'runtime',
  value_enc      TEXT NOT NULL,
  value_digest   TEXT NOT NULL DEFAULT '',
  active         INTEGER NOT NULL DEFAULT 1,
  created_by     TEXT NOT NULL DEFAULT 'migration',
  created_at     INTEGER NOT NULL,
  UNIQUE(environment_id, key, revision)
);
CREATE INDEX IF NOT EXISTS idx_deploy_variable_active
  ON deploy_variable_revisions(environment_id, active, key);

-- A run binds to the exact variable revisions visible when it is enqueued.
-- The header exists even for an empty set, which distinguishes a deliberately
-- empty snapshot from an old or corrupt run that was never snapshotted.
CREATE TABLE IF NOT EXISTS deploy_run_variable_snapshots (
  run_id             INTEGER PRIMARY KEY REFERENCES deploy_runs(id) ON DELETE CASCADE,
  environment_id     INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  digest             TEXT NOT NULL,
  copied_from_run_id INTEGER NOT NULL DEFAULT 0,
  created_at         INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS deploy_run_variable_revisions (
  run_id               INTEGER NOT NULL REFERENCES deploy_runs(id) ON DELETE CASCADE,
  variable_revision_id INTEGER NOT NULL REFERENCES deploy_variable_revisions(id),
  ordinal              INTEGER NOT NULL,
  PRIMARY KEY(run_id, variable_revision_id),
  UNIQUE(run_id, ordinal)
);
CREATE INDEX IF NOT EXISTS idx_deploy_run_variable_revision
  ON deploy_run_variable_revisions(variable_revision_id, run_id);

CREATE TRIGGER IF NOT EXISTS deploy_run_variable_snapshot_immutable
BEFORE UPDATE ON deploy_run_variable_snapshots
BEGIN
  SELECT RAISE(ABORT, 'deployment run variable snapshot is immutable');
END;
CREATE TRIGGER IF NOT EXISTS deploy_run_variable_reference_immutable
BEFORE UPDATE ON deploy_run_variable_revisions
BEGIN
  SELECT RAISE(ABORT, 'deployment run variable reference is immutable');
END;

-- Dependencies and checks are captured with the run for the same reason as
-- variable revisions: saving a later desired plan must not rewrite queued or
-- retried execution input. JSON is canonicalized by the enqueue path and is
-- secret-free by plan validation.
CREATE TABLE IF NOT EXISTS deploy_run_plan_snapshots (
  run_id             INTEGER PRIMARY KEY REFERENCES deploy_runs(id) ON DELETE CASCADE,
  environment_id     INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  dependencies_json  TEXT NOT NULL DEFAULT '[]',
  checks_json        TEXT NOT NULL DEFAULT '[]',
  digest             TEXT NOT NULL,
  copied_from_run_id INTEGER NOT NULL DEFAULT 0,
  created_at         INTEGER NOT NULL
);
CREATE TRIGGER IF NOT EXISTS deploy_run_plan_snapshot_immutable
BEFORE UPDATE ON deploy_run_plan_snapshots
BEGIN
  SELECT RAISE(ABORT, 'deployment run plan snapshot is immutable');
END;

CREATE TABLE IF NOT EXISTS deploy_blueprint_installs (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  environment_id    INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  blueprint_id      TEXT NOT NULL,
  blueprint_version TEXT NOT NULL,
  inputs_json       TEXT NOT NULL DEFAULT '{}',
  plan_digest       TEXT NOT NULL,
  installed_at      INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL,
  UNIQUE(environment_id, blueprint_id)
);

CREATE TABLE IF NOT EXISTS deploy_port_leases (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  environment_id INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  run_id         INTEGER NOT NULL DEFAULT 0,
  address        TEXT NOT NULL DEFAULT '127.0.0.1',
  port           INTEGER NOT NULL,
  protocol       TEXT NOT NULL DEFAULT 'tcp',
  purpose        TEXT NOT NULL DEFAULT '',
  token          TEXT NOT NULL UNIQUE,
  expires_at     INTEGER NOT NULL,
  created_at     INTEGER NOT NULL,
  UNIQUE(address, port, protocol)
);

-- Removal is distinct from archiving and may finish target by target. These
-- receipts make a partial retry idempotent without deleting the ownership
-- graph that explains what a historical release used.
CREATE TABLE IF NOT EXISTS deploy_resource_removals (
  project_id  INTEGER NOT NULL REFERENCES deploy_projects(id) ON DELETE CASCADE,
  target_id   TEXT NOT NULL,
  target_kind TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  removed_by  TEXT NOT NULL,
  removed_at  INTEGER NOT NULL,
  PRIMARY KEY(project_id, target_id)
);

CREATE TABLE IF NOT EXISTS deploy_queue_leases (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id         INTEGER NOT NULL UNIQUE REFERENCES deploy_runs(id) ON DELETE CASCADE,
  environment_id INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  claim_token    TEXT NOT NULL UNIQUE,
  slot_class     TEXT NOT NULL DEFAULT 'light',
  claimed_by     TEXT NOT NULL,
  expires_at     INTEGER NOT NULL,
  heartbeat_at   INTEGER NOT NULL,
  created_at     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_deploy_queue_expiry ON deploy_queue_leases(expires_at);

CREATE TABLE IF NOT EXISTS deploy_drafts (
  id                   TEXT PRIMARY KEY,
  owner_user_id        INTEGER NOT NULL,
  owner_username       TEXT NOT NULL,
  current_step         TEXT NOT NULL DEFAULT 'intent',
  revision             INTEGER NOT NULL DEFAULT 1,
  data_json            TEXT NOT NULL DEFAULT '{}',
  findings_json        TEXT NOT NULL DEFAULT '[]',
  plan_preview         TEXT NOT NULL DEFAULT '',
  committed_project_id INTEGER NOT NULL DEFAULT 0,
  created_at           INTEGER NOT NULL,
  updated_at           INTEGER NOT NULL,
  expires_at           INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS deploy_schedules (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  environment_id INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  name           TEXT NOT NULL,
  expression     TEXT NOT NULL,
  timezone       TEXT NOT NULL DEFAULT 'UTC',
  enabled        INTEGER NOT NULL DEFAULT 1,
  next_run_at    INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  UNIQUE(environment_id, name)
);

CREATE TABLE IF NOT EXISTS deploy_schedule_steps (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  schedule_id INTEGER NOT NULL REFERENCES deploy_schedules(id) ON DELETE CASCADE,
  ordinal     INTEGER NOT NULL,
  action      TEXT NOT NULL,
  config_json TEXT NOT NULL DEFAULT '{}',
  required    INTEGER NOT NULL DEFAULT 1,
  UNIQUE(schedule_id, ordinal)
);

CREATE TABLE IF NOT EXISTS deploy_notification_channels (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT NOT NULL UNIQUE,
  url         TEXT NOT NULL,
  headers_enc TEXT NOT NULL DEFAULT '',
  secret_enc  TEXT NOT NULL DEFAULT '',
  events      TEXT NOT NULL DEFAULT '',
  enabled     INTEGER NOT NULL DEFAULT 1,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS deploy_notification_deliveries (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  channel_id      INTEGER NOT NULL REFERENCES deploy_notification_channels(id) ON DELETE CASCADE,
  run_id          INTEGER NOT NULL DEFAULT 0,
  event           TEXT NOT NULL,
  attempt         INTEGER NOT NULL DEFAULT 1,
  status          TEXT NOT NULL,
  response_class  TEXT NOT NULL DEFAULT '',
  next_attempt_at INTEGER NOT NULL DEFAULT 0,
  created_at      INTEGER NOT NULL,
  completed_at    INTEGER NOT NULL DEFAULT 0,
  UNIQUE(channel_id, run_id, event, attempt)
);

CREATE TABLE IF NOT EXISTS deploy_preview_refs (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  trigger_id     INTEGER NOT NULL REFERENCES deploy_triggers(id) ON DELETE CASCADE,
  provider_ref   TEXT NOT NULL,
  environment_id INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  state          TEXT NOT NULL DEFAULT 'open',
  updated_at     INTEGER NOT NULL,
  UNIQUE(trigger_id, provider_ref)
);

CREATE TABLE IF NOT EXISTS watched_domains (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  domain     TEXT NOT NULL UNIQUE,
  port       INTEGER NOT NULL DEFAULT 443,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- Host utilisation, sampled by the server on its own timer.
--
-- The charts used to exist only inside the browser tab that drew them, so the
-- history began when the page was opened and died with it: a spike at 03:00
-- was unobservable unless somebody happened to be watching at 03:00. That is
-- the opposite of what a monitoring page is for. Sampling here, driven by the
-- backend rather than by a client, is what makes "what happened while I was
-- asleep" answerable at all.
--
-- ts is the rowid, so range scans over a window are a b-tree walk and a
-- restarted sampler landing on the same second replaces rather than
-- duplicates. Rows are pruned past JD_METRICS_RETENTION.
-- Per-filesystem capacity, recorded by the same sampler.
--
-- The host table keeps only the fullest mount, which is a summary and not an
-- answer: when the fullest filesystem stops being the fullest, that single
-- line drops by whatever separates it from the runner-up, and reads as
-- somebody having freed a great deal of space on a disk that did not change.
-- Which filesystem grew is the actual question, so each one gets its own row.
--
-- Cardinality is small on purpose: pseudo filesystems are filtered out before
-- this is written, so a machine records a handful of rows per sample, not one
-- per cgroup mount.
CREATE TABLE IF NOT EXISTS metric_mount_samples (
  ts           INTEGER NOT NULL,
  mountpoint   TEXT NOT NULL,
  used_percent REAL NOT NULL DEFAULT 0,
  used_bytes   INTEGER NOT NULL DEFAULT 0,
  total_bytes  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (mountpoint, ts)
);
-- Pruning walks by time across every mount, which the primary key cannot serve.
CREATE INDEX IF NOT EXISTS idx_mount_samples_ts ON metric_mount_samples(ts);

-- Per-container utilisation, recorded by the same sampler.
--
-- Keyed by container name rather than id on purpose: a compose redeploy
-- replaces a container with a new id under the same name, and seeing across
-- that restart is most of the reason to keep the history. An id would start a
-- fresh, empty series every deploy — exactly the amnesia this table exists to
-- cure.
CREATE TABLE IF NOT EXISTS metric_container_samples (
  ts          INTEGER NOT NULL,
  name        TEXT NOT NULL,
  cpu_percent REAL NOT NULL DEFAULT 0,
  mem_bytes   INTEGER NOT NULL DEFAULT 0,
  mem_limit   INTEGER NOT NULL DEFAULT 0,
  mem_percent REAL NOT NULL DEFAULT 0,
  net_rx      INTEGER NOT NULL DEFAULT 0,
  net_tx      INTEGER NOT NULL DEFAULT 0,
  pids        INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (name, ts)
);
-- Pruning walks by time across every container, which the primary key (name
-- first) cannot serve.
CREATE INDEX IF NOT EXISTS idx_container_samples_ts ON metric_container_samples(ts);

CREATE TABLE IF NOT EXISTS metric_samples (
  ts             INTEGER PRIMARY KEY,
  cpu_percent    REAL NOT NULL DEFAULT 0,
  load1          REAL NOT NULL DEFAULT 0,
  mem_percent    REAL NOT NULL DEFAULT 0,
  mem_used       INTEGER NOT NULL DEFAULT 0,
  mem_total      INTEGER NOT NULL DEFAULT 0,
  swap_percent   REAL NOT NULL DEFAULT 0,
  net_rx         REAL NOT NULL DEFAULT 0,
  net_tx         REAL NOT NULL DEFAULT 0,
  disk_read      REAL NOT NULL DEFAULT 0,
  disk_write     REAL NOT NULL DEFAULT 0,
  disk_percent   REAL NOT NULL DEFAULT 0,
  uptime_seconds INTEGER NOT NULL DEFAULT 0
);
`

// Indexes that name added columns must run after applyAddedColumns. Putting
// them in schema makes an upgrade fail before the ALTERs can add those columns
// to a 0.6.6 deploy_runs table; a fresh database reaches the same final shape
// through this second block.
const postColumnSchema = `
CREATE INDEX IF NOT EXISTS idx_container_samples_identity_ts
  ON metric_container_samples(container_id, ts);
CREATE INDEX IF NOT EXISTS idx_deploy_runs_queue
  ON deploy_runs(state, priority, requested_at, id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_deploy_runs_idempotency
  ON deploy_runs(project_id, environment_id, operation, idempotency_key)
  WHERE idempotency_key <> '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_deploy_queue_environment
  ON deploy_queue_leases(environment_id);
CREATE TRIGGER IF NOT EXISTS deploy_release_expected_downtime_immutable
BEFORE UPDATE OF expected_downtime ON deploy_releases
BEGIN
  SELECT RAISE(ABORT, 'deployment release snapshot is immutable');
END;
CREATE TRIGGER IF NOT EXISTS deploy_variable_revision_identity_immutable
BEFORE UPDATE OF environment_id, key, revision, sensitivity, scopes, value_enc,
  value_digest, created_by, created_at ON deploy_variable_revisions
BEGIN
  SELECT RAISE(ABORT, 'deployment variable revision is immutable');
END;
`

// addedColumns are columns that arrived after the table they belong to had
// already shipped.
//
// A CREATE TABLE IF NOT EXISTS is a no-op against a database that predates
// them, so every one of these has to be applied separately — this is what
// "schema changes must be additive" means in practice, given there is no
// migration tool. Each entry must therefore carry a DEFAULT: an existing table
// has rows, and SQLite will not add a NOT NULL column to them without one.
//
// Never remove an entry. It is not a list of the current schema, it is the
// list of steps between every shipped schema and the current one, and dropping
// one strands whichever installs stopped at that version.
var addedColumns = []struct{ table, column, spec string }{
	// 0.6.7 keeps the shipped project/run rows as stable compatibility
	// identities while normalized environments, releases, steps and events
	// grow beside them. Every legacy row receives a usable zero/default before
	// the transactional mapper fills its production environment.
	{"deploy_projects", "profile", "TEXT NOT NULL DEFAULT 'compose'"},
	{"deploy_projects", "updated_at", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_projects", "archived_at", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_runs", "environment_id", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_runs", "state", "TEXT NOT NULL DEFAULT ''"},
	{"deploy_runs", "operation", "TEXT NOT NULL DEFAULT 'deploy'"},
	{"deploy_runs", "requested_at", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_runs", "queued_at", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_runs", "claimed_at", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_runs", "heartbeat_at", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_runs", "cancel_requested", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_runs", "superseded_by", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_runs", "retry_of_run_id", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_runs", "idempotency_key", "TEXT NOT NULL DEFAULT ''"},
	{"deploy_runs", "request_digest", "TEXT NOT NULL DEFAULT ''"},
	{"deploy_runs", "plan_revision", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_runs", "release_id", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_runs", "candidate_release_id", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_runs", "terminal_code", "TEXT NOT NULL DEFAULT ''"},
	{"deploy_runs", "terminal_reason", "TEXT NOT NULL DEFAULT ''"},
	{"deploy_runs", "lease_token", "TEXT NOT NULL DEFAULT ''"},
	{"deploy_runs", "lease_until", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_runs", "priority", "INTEGER NOT NULL DEFAULT 100"},
	{"deploy_runs", "slot_class", "TEXT NOT NULL DEFAULT 'light'"},
	{"deploy_runs", "metadata_json", "TEXT NOT NULL DEFAULT '{}'"},
	{"deploy_releases", "expected_downtime", "INTEGER NOT NULL DEFAULT 1"},

	// The CPU mode breakdown, added because one "busy" percentage cannot tell
	// apart a server doing work, a server waiting on a disk, and a hypervisor
	// running somebody else on the core you are paying for.
	{"metric_samples", "cpu_user", "REAL NOT NULL DEFAULT 0"},
	{"metric_samples", "cpu_system", "REAL NOT NULL DEFAULT 0"},
	{"metric_samples", "cpu_iowait", "REAL NOT NULL DEFAULT 0"},
	{"metric_samples", "cpu_steal", "REAL NOT NULL DEFAULT 0"},

	// Pressure stall information: the kernel's own measure of whether tasks
	// are waiting rather than running, which utilisation cannot express.
	{"metric_samples", "psi_cpu", "REAL NOT NULL DEFAULT 0"},
	{"metric_samples", "psi_mem", "REAL NOT NULL DEFAULT 0"},
	{"metric_samples", "psi_io", "REAL NOT NULL DEFAULT 0"},

	// Operations per second and service time. A disk saturated by small
	// random writes moves almost no bytes, so the byte rates alone report an
	// idle disk that is in fact unable to take another request.
	{"metric_samples", "disk_reads", "REAL NOT NULL DEFAULT 0"},
	{"metric_samples", "disk_writes", "REAL NOT NULL DEFAULT 0"},
	{"metric_samples", "disk_await", "REAL NOT NULL DEFAULT 0"},
	{"metric_samples", "disk_busy", "REAL NOT NULL DEFAULT 0"},

	// Sockets, so "we ran out of connections at 3am" leaves a trace.
	{"metric_samples", "tcp_conns", "INTEGER NOT NULL DEFAULT 0"},
	{"metric_samples", "tcp_timewait", "INTEGER NOT NULL DEFAULT 0"},

	// The other two load averages. One-minute load alone cannot distinguish a
	// spike that is ending from one that is building.
	{"metric_samples", "load5", "REAL NOT NULL DEFAULT 0"},
	{"metric_samples", "load15", "REAL NOT NULL DEFAULT 0"},
	{"metric_samples", "mem_available", "INTEGER NOT NULL DEFAULT 0"},
	{"metric_samples", "procs", "INTEGER NOT NULL DEFAULT 0"},

	// A container's network and block throughput, which was already being
	// sampled for the live view but never kept.
	{"metric_container_samples", "block_read", "INTEGER NOT NULL DEFAULT 0"},
	{"metric_container_samples", "block_write", "INTEGER NOT NULL DEFAULT 0"},
	// Name continuity serves Docker charts; release attribution needs the exact
	// observed container identity. Old samples deliberately remain unattributed.
	{"metric_container_samples", "container_id", "TEXT NOT NULL DEFAULT ''"},

	// Inode exhaustion fills a filesystem that reports free space, and is
	// invisible in a used-bytes percentage.
	{"metric_mount_samples", "inodes_percent", "REAL NOT NULL DEFAULT 0"},
}

// applyAddedColumns adds any column the running binary expects and the file on
// disk does not have.
//
// It reads the table's current shape rather than trying the ALTER and
// swallowing the error: "duplicate column name" is a string comparison against
// a message SQLite is free to reword, and a real failure would look identical.
func applyAddedColumns(ctx context.Context, db *sql.DB) error {
	have := map[string]map[string]bool{}
	for _, c := range addedColumns {
		if have[c.table] != nil {
			continue
		}
		cols, err := tableColumns(ctx, db, c.table)
		if err != nil {
			return fmt.Errorf("read %s columns: %w", c.table, err)
		}
		have[c.table] = cols
	}
	for _, c := range addedColumns {
		if have[c.table][c.column] {
			continue
		}
		// The table name is a constant in this file, never user input, so
		// there is nothing here to parameterise — and SQLite does not accept
		// a placeholder for an identifier in any case.
		stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", c.table, c.column, c.spec)
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("add %s.%s: %w", c.table, c.column, err)
		}
		have[c.table][c.column] = true
	}
	return nil
}

func tableColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	path := filepath.Join(dataDir, DatabaseFile)
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// SQLite tolerates a single writer; the pool is kept small so the WAL
	// writer never contends with itself under WebSocket fan-out.
	db.SetMaxOpenConns(4)
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	// Run after the schema, never instead of it: a fresh database gets its
	// tables from the block above and finds nothing to add, while an existing
	// one gets only the columns it is missing.
	if err := applyAddedColumns(context.Background(), db); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(context.Background(), postColumnSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply post-column schema: %w", err)
	}
	// Normalized deployment records are derived only after every compatibility
	// column and index exists. The mapper is transactional and uniquely keyed,
	// so opening the same 0.6.6 database repeatedly is a no-op after the first.
	if err := migrateLegacyDeployments(context.Background(), db); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) Setting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}
