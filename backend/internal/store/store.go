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
	DB    *sql.DB
	guard *walGuard
}

// DatabaseFile is the SQLite file inside the data directory. It keeps its
// original name through the "Just Dashboard" rename: moving it would buy
// nothing and would strand every existing install's accounts, audit log and
// encrypted secrets. config.Load looks for it by this name when deciding
// whether a pre-rename data directory should be adopted.
const DatabaseFile = "vpsd.db"

const schema = sleepSchema + runDiagnosisSchema + rehearsalSchema + dataRollbackSchema + previewBranchSchema + imageScanSchema + rollbackWatchSchema + deployEventSchema + serverMapSchema + sharedVariableSchema + secretStoreSchema + passkeySchema + accountLinkSchema + networkProbeSchema + remoteDesktopSchema + hostAlertSchema + networkIPAMSchema + networkWireGuardSchema + networkIncidentSchema + networkGatewaySchema + networkRouteHistorySchema + firewallHistorySchema + networkEgressSchema + networkTrafficSchema + `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS users (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  username       TEXT NOT NULL UNIQUE,
  password_hash  TEXT NOT NULL,
  role           TEXT NOT NULL,
  totp_secret    TEXT NOT NULL DEFAULT '',
  totp_enabled   INTEGER NOT NULL DEFAULT 0,
  totp_last_step INTEGER NOT NULL DEFAULT -1,
  disabled       INTEGER NOT NULL DEFAULT 0,
  must_change_pw INTEGER NOT NULL DEFAULT 0,
  failed_count   INTEGER NOT NULL DEFAULT 0,
  locked_until   INTEGER NOT NULL DEFAULT 0,
  last_login_at  INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL,
  display_name   TEXT NOT NULL DEFAULT '',
  avatar         BLOB NOT NULL DEFAULT x'',
  avatar_type    TEXT NOT NULL DEFAULT '',
  avatar_at      INTEGER NOT NULL DEFAULT 0,
  needs_onboarding INTEGER NOT NULL DEFAULT 0,
  hidden_sections  TEXT NOT NULL DEFAULT '[]'
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
  auth_method  TEXT NOT NULL DEFAULT 'unknown',
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
  -- '' opens every route the key's role allows; a capability such as
  -- 'metrics.read' opens only the routes that name it; 'mcp' opens the MCP
  -- endpoint and reaches only the databases api_token_connections grants it;
  -- 'deploy' opens the deployment routes its table lists, and only the
  -- deployments api_token_deployments names when it names any.
  scope        TEXT NOT NULL DEFAULT '',
  -- What an MCP key may do with the deployments: '' nothing, 'read' read
  -- them and their runs, 'deploy' also queue deploys and rollbacks.
  deploy_access TEXT NOT NULL DEFAULT '',
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
  detail    TEXT NOT NULL DEFAULT '',
  -- The API key behind the request, when one was: which of an account's keys
  -- acted is the question a leaked key raises, and the account alone cannot
  -- answer it. The name is copied, so a key deleted later still reads.
  key_id    INTEGER NOT NULL DEFAULT 0,
  key_name  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_log(ts DESC);
CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_log(user_id);

CREATE TABLE IF NOT EXISTS db_connections (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL UNIQUE,
  driver     TEXT NOT NULL,
  dsn_enc    TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  -- What the operator says about the connection, as opposed to what its DSN
  -- says: which environment it serves, whether the dashboard may change what
  -- is in it, and anything worth remembering about it.
  environment TEXT NOT NULL DEFAULT '',
  read_only   INTEGER NOT NULL DEFAULT 0,
  notes       TEXT NOT NULL DEFAULT ''
);

-- The databases an MCP key reaches, and whether it may change what is in
-- each ('read' or 'write'). A key reaches nothing that has no row here, and
-- forgetting the connection or deleting the key's record takes the row with it.
CREATE TABLE IF NOT EXISTS api_token_connections (
  token_id      INTEGER NOT NULL REFERENCES api_tokens(id) ON DELETE CASCADE,
  connection_id INTEGER NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
  access        TEXT NOT NULL DEFAULT 'read',
  PRIMARY KEY (token_id, connection_id)
);
CREATE INDEX IF NOT EXISTS idx_api_token_connections_conn ON api_token_connections(connection_id);

-- The deployments a deploy key reaches. None is every deployment: a key for
-- one pipeline names its own, a key for an operator's laptop names none. The
-- project is deliberately not a foreign key: a deployment deleted for good
-- leaves its row behind, so a key that reached only it goes on reaching
-- nothing, where a cascade would have emptied the list into "everything".
CREATE TABLE IF NOT EXISTS api_token_deployments (
  token_id   INTEGER NOT NULL REFERENCES api_tokens(id) ON DELETE CASCADE,
  project_id INTEGER NOT NULL,
  PRIMARY KEY (token_id, project_id)
);
CREATE INDEX IF NOT EXISTS idx_api_token_deployments_project ON api_token_deployments(project_id);

-- Databases brought over from another server. A replication import outlives the
-- request that started it by hours, so it is a row: the target keeps the
-- subscription and the source keeps the slot, and this is what finds both again
-- to cut over or take them away. The source's connection string is what the
-- cutover needs and is sealed like a saved connection's; it is emptied when the
-- import ends, and no route returns it. source_label is the address without the
-- credentials, which is all the page shows.
CREATE TABLE IF NOT EXISTS db_online_imports (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  connection_id    INTEGER NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
  name             TEXT NOT NULL,
  mode             TEXT NOT NULL,
  status           TEXT NOT NULL,
  target_database  TEXT NOT NULL,
  source_label     TEXT NOT NULL,
  source_user      TEXT NOT NULL DEFAULT '',
  source_enc       TEXT NOT NULL DEFAULT '',
  structure_only   INTEGER NOT NULL DEFAULT 0,
  publication_made INTEGER NOT NULL DEFAULT 0,
  detail           TEXT NOT NULL DEFAULT '',
  started_by       TEXT NOT NULL DEFAULT '',
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL,
  finished_at      INTEGER NOT NULL DEFAULT 0
);
-- A name is what the publication, the subscription and the slot are called, so
-- it is taken only while an import holds them.
CREATE UNIQUE INDEX IF NOT EXISTS idx_db_online_imports_live
  ON db_online_imports(connection_id, name) WHERE status IN ('running', 'replicating', 'cutting');

-- Background activity history is tied to the exact saved target.
CREATE TABLE IF NOT EXISTS db_metric_samples (
  connection_id INTEGER NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
  identity      TEXT NOT NULL,
  at            INTEGER NOT NULL,
  snapshot      TEXT NOT NULL,
  PRIMARY KEY (connection_id, identity, at)
);
CREATE INDEX IF NOT EXISTS idx_db_metric_samples_at ON db_metric_samples(at);

-- Statement history: what each statement added between two readings of the
-- statement list, taken every five minutes and kept for seven days, tied to
-- the exact saved target as the activity history is. An interval is one
-- reading's whole, with the time of every statement the engine counted; a
-- sample is one statement's part of it, for the fifty that took the most.
-- A shape is the statement's text as the statement list shows it, every value
-- replaced, and never one execution of it. New tables are additive.
CREATE TABLE IF NOT EXISTS db_statement_intervals (
  connection_id INTEGER NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
  identity      TEXT NOT NULL,
  at            INTEGER NOT NULL,
  seconds       INTEGER NOT NULL,
  total_ms      REAL NOT NULL,
  reset         INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (connection_id, identity, at)
);
CREATE INDEX IF NOT EXISTS idx_db_statement_intervals_at ON db_statement_intervals(at);
CREATE TABLE IF NOT EXISTS db_statement_shapes (
  id            INTEGER PRIMARY KEY,
  connection_id INTEGER NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
  identity      TEXT NOT NULL,
  statement     TEXT NOT NULL,
  query         TEXT NOT NULL,
  seen_at       INTEGER NOT NULL,
  UNIQUE (connection_id, identity, statement)
);
CREATE TABLE IF NOT EXISTS db_statement_samples (
  shape_id  INTEGER NOT NULL REFERENCES db_statement_shapes(id) ON DELETE CASCADE,
  at        INTEGER NOT NULL,
  calls     INTEGER NOT NULL,
  total_ms  REAL NOT NULL,
  returned  INTEGER NOT NULL,
  examined  INTEGER,
  durations TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (shape_id, at)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_db_statement_samples_at ON db_statement_samples(at);

-- The certificate authority that signs the server certificates of the databases
-- this dashboard started. There is at most one, made the first time TLS is
-- switched on; its certificate is public and kept as it is, its key is sealed
-- with the master key like every other secret at rest.
CREATE TABLE IF NOT EXISTS db_tls_ca (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  cert_pem    TEXT NOT NULL,
  key_enc     TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  not_after   INTEGER NOT NULL,
  created_at  INTEGER NOT NULL
);

-- A database server that speaks TLS with a certificate from that authority:
-- what it was issued for and when it ends, so that renewal can be decided
-- without opening the container. The certificate's key lives only in the
-- container's own volume and is replaced whenever the certificate is.
CREATE TABLE IF NOT EXISTS db_server_tls (
  connection_id INTEGER PRIMARY KEY REFERENCES db_connections(id) ON DELETE CASCADE,
  container     TEXT NOT NULL,
  require_tls   INTEGER NOT NULL DEFAULT 0,
  names         TEXT NOT NULL DEFAULT '[]',
  serial        TEXT NOT NULL DEFAULT '',
  issued_at     INTEGER NOT NULL,
  not_after     INTEGER NOT NULL,
  renewed_at    INTEGER NOT NULL DEFAULT 0
);

-- Database alerts. A rule watches one database, or every database when
-- connection_id is NULL, for one kind of trouble (the server down, its disk,
-- its connections, its replication, a backup failed or missed) against a
-- line — threshold in the kind's unit, minutes it must hold or a backup's
-- grace — and tells the notification channels it names, every enabled one
-- when channels = '[]'. revision moves on every edit, so a verdict reached
-- on the old terms is never written over the new ones.
CREATE TABLE IF NOT EXISTS db_alert_rules (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  connection_id    INTEGER REFERENCES db_connections(id) ON DELETE CASCADE,
  kind             TEXT NOT NULL,
  threshold        REAL NOT NULL DEFAULT 0,
  minutes          INTEGER NOT NULL DEFAULT 0,
  cooldown_minutes INTEGER NOT NULL DEFAULT 0,
  notify_recovery  INTEGER NOT NULL DEFAULT 1,
  channels         TEXT NOT NULL DEFAULT '[]',
  enabled          INTEGER NOT NULL DEFAULT 1,
  revision         INTEGER NOT NULL DEFAULT 1,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_db_alert_rules_conn ON db_alert_rules(connection_id);
-- One subject a rule watches — a database, or a backup job that dumps one —
-- as the evaluator last read it. since is when the condition began to hold,
-- fired_at when it had held long enough to be an alert, notified_at when
-- that was told (0 while the cooldown keeps it back), told_at the last time
-- this subject's firing was told at all, which the cooldown counts from.
-- seen and clear count the passes in a row over the line and under its
-- clearing line. unknown says why the last pass could not judge it.
CREATE TABLE IF NOT EXISTS db_alert_state (
  rule_id       INTEGER NOT NULL REFERENCES db_alert_rules(id) ON DELETE CASCADE,
  subject       TEXT NOT NULL,
  connection_id INTEGER REFERENCES db_connections(id) ON DELETE CASCADE,
  job_id        INTEGER NOT NULL DEFAULT 0,
  label         TEXT NOT NULL DEFAULT '',
  reading       REAL NOT NULL DEFAULT 0,
  detail        TEXT NOT NULL DEFAULT '',
  unknown       TEXT NOT NULL DEFAULT '',
  seen          INTEGER NOT NULL DEFAULT 0,
  clear         INTEGER NOT NULL DEFAULT 0,
  since         INTEGER NOT NULL DEFAULT 0,
  fired_at      INTEGER NOT NULL DEFAULT 0,
  notified_at   INTEGER NOT NULL DEFAULT 0,
  told_at       INTEGER NOT NULL DEFAULT 0,
  checked_at    INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (rule_id, subject)
);
-- What each pass told, or kept back and why (kept: 'cooldown', 'recovery'
-- for a rule that does not tell recoveries), for the Alerts page. The rule
-- may since be gone; the newest 1000 are kept.
CREATE TABLE IF NOT EXISTS db_alert_events (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  rule_id       INTEGER NOT NULL,
  connection_id INTEGER REFERENCES db_connections(id) ON DELETE CASCADE,
  job_id        INTEGER NOT NULL DEFAULT 0,
  kind          TEXT NOT NULL,
  subject       TEXT NOT NULL,
  label         TEXT NOT NULL DEFAULT '',
  event         TEXT NOT NULL,
  detail        TEXT NOT NULL DEFAULT '',
  delivered     INTEGER NOT NULL DEFAULT 0,
  failed        INTEGER NOT NULL DEFAULT 0,
  kept          TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_db_alert_events_conn ON db_alert_events(connection_id, id);

-- Named SQL snippets an operator keeps against a connection. The SQL is stored
-- in the clear: it is a query the operator wrote, not a credential, and the
-- connection it runs against carries the secret. A NULL connection_id would be
-- a snippet shared across connections, but membership is kept concrete for now.
CREATE TABLE IF NOT EXISTS db_saved_queries (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  connection_id INTEGER NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  sql           TEXT NOT NULL,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL DEFAULT 0
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
  ran_at        INTEGER NOT NULL,
  rows_affected INTEGER NOT NULL DEFAULT 0,
  error         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_db_history_conn ON db_query_history(connection_id, ran_at DESC);

-- How an operator arranged one schema's diagram: where each table sits, which
-- are hidden, the notes and colours they added. One JSON document per
-- (connection, schema) rather than a row per table, because the diagram reads
-- and writes it as a whole and nothing else reads it at all. The server only
-- checks that it is a JSON object under a size cap; every field in it is a
-- decision about a picture, and the picture is the only thing that decodes it.
-- Nothing in it is secret — it names tables the reader can already list.
CREATE TABLE IF NOT EXISTS db_diagram_layouts (
  connection_id INTEGER NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
  schema_name   TEXT NOT NULL DEFAULT '',
  layout        TEXT NOT NULL,
  updated_at    INTEGER NOT NULL,
  PRIMARY KEY (connection_id, schema_name)
);

-- The connection pooler (PgBouncer) the dashboard runs beside a PostgreSQL
-- connection's container: where it is, which database container it forwards to
-- and by what name, how it was asked to behave, and a digest of the two files
-- it was last given so a changed password is noticed.
-- The accounts PgBouncer signs in and reads statistics with are sealed
-- (secrets_enc); the connection's own password is read from the connection.
CREATE TABLE IF NOT EXISTS db_poolers (
  connection_id   INTEGER PRIMARY KEY REFERENCES db_connections(id) ON DELETE CASCADE,
  container_name  TEXT NOT NULL,
  container_id    TEXT NOT NULL DEFAULT '',
  network_name    TEXT NOT NULL,
  host_port       INTEGER NOT NULL DEFAULT 0,
  server_name     TEXT NOT NULL,
  server_port     INTEGER NOT NULL,
  pool_mode       TEXT NOT NULL,
  pool_size       INTEGER NOT NULL,
  max_client_conn INTEGER NOT NULL,
  auth_mode       TEXT NOT NULL,
  secrets_enc     TEXT NOT NULL,
  config_sha      TEXT NOT NULL DEFAULT '',
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);

-- Databases found on this machine that the operator said to leave alone. The
-- key is the inventory's own (docker:<container>, compose:<project>/<service>,
-- host:<unit>, file:<path>), not a connection id: the point of the row is to
-- outlive the connection that was forgotten, so the next reconcile does not
-- quietly bring it back.
CREATE TABLE IF NOT EXISTS db_inventory_ignored (
  origin     TEXT PRIMARY KEY,
  ignored_at INTEGER NOT NULL,
  ignored_by TEXT NOT NULL DEFAULT ''
);

-- A major-version upgrade of a database server the dashboard started: the new
-- server is a new container under the old one's name, and the old one is kept
-- stopped beside it under another until the operator finishes or reverts. The
-- row is what says which container is which for those days, and what a
-- dashboard restarted in the middle of an upgrade has to put back. state is
-- running, upgraded (the revert window is open), finished, reverted or
-- failed; phase is how far a running one got (prepare, copy, cutover). Names, never
-- credentials: the password stays in the sealed DSN and the containers.
CREATE TABLE IF NOT EXISTS db_upgrades (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  connection_id     INTEGER NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
  state             TEXT NOT NULL,
  phase             TEXT NOT NULL DEFAULT '',
  engine            TEXT NOT NULL,
  from_version      TEXT NOT NULL,
  to_version        TEXT NOT NULL,
  from_image        TEXT NOT NULL,
  to_image          TEXT NOT NULL,
  container         TEXT NOT NULL,
  previous_id       TEXT NOT NULL DEFAULT '',
  previous_name     TEXT NOT NULL DEFAULT '',
  previous_restart  TEXT NOT NULL DEFAULT '',
  previous_volume   TEXT NOT NULL DEFAULT '',
  candidate         TEXT NOT NULL DEFAULT '',
  volume            TEXT NOT NULL DEFAULT '',
  safety_dump       TEXT NOT NULL DEFAULT '',
  -- What postgresql.auto.conf gave default_transaction_read_only before the
  -- copy made the old server take reads only, so it is given back exactly.
  read_only_prior   TEXT NOT NULL DEFAULT '',
  error             TEXT NOT NULL DEFAULT '',
  started_by        TEXT NOT NULL DEFAULT '',
  started_at        INTEGER NOT NULL,
  upgraded_at       INTEGER NOT NULL DEFAULT 0,
  ended_at          INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_db_upgrades_conn ON db_upgrades(connection_id, id DESC);

-- Point-in-time recovery: a PostgreSQL server's write-ahead log archived by
-- WAL-G into a backup destination, beside scheduled base backups. One row per
-- archive: the server it is for, by container name (which survives the
-- container being recreated), the folder inside the destination it writes,
-- the destination with its keys sealed as a backup job's are, and the key
-- pair the archive is encrypted with — the public half in the clear, because
-- that is all a server is handed, and the private half sealed. A server
-- writes into at most one archive (ended_at = 0). The others are kept for as
-- long as what they hold is: a moment from before a switch is only in the
-- archive the server had then. reading is the archive's last listing, so the
-- page can say what it covers without asking the destination on every poll.
CREATE TABLE IF NOT EXISTS db_wal_archives (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  container         TEXT NOT NULL,
  connection_id     INTEGER NOT NULL DEFAULT 0,
  stream            TEXT NOT NULL,
  target_kind       TEXT NOT NULL,
  target_cfg        TEXT NOT NULL DEFAULT '{}',
  secrets_enc       TEXT NOT NULL DEFAULT '',
  public_key        TEXT NOT NULL,
  private_key_enc   TEXT NOT NULL,
  schedule          TEXT NOT NULL DEFAULT '',
  retention_days    INTEGER NOT NULL DEFAULT 7,
  image             TEXT NOT NULL DEFAULT '',
  tool              TEXT NOT NULL DEFAULT '',
  created_at        INTEGER NOT NULL,
  created_by        TEXT NOT NULL DEFAULT '',
  ended_at          INTEGER NOT NULL DEFAULT 0,
  reading           TEXT NOT NULL DEFAULT '',
  read_at           INTEGER NOT NULL DEFAULT 0,
  last_backup_at    INTEGER NOT NULL DEFAULT 0,
  last_backup_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_db_wal_archives_container ON db_wal_archives(container, ended_at);

-- Moments named in an archive's log with pg_create_restore_point, so a
-- recovery can be asked to stop at one by the name it was given here.
CREATE TABLE IF NOT EXISTS db_wal_restore_points (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  archive_id INTEGER NOT NULL REFERENCES db_wal_archives(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  lsn        TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  created_by TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_db_wal_restore_points_archive ON db_wal_restore_points(archive_id, created_at DESC);

-- A copy recovered from an archive into a container of its own, and what
-- became of it: kept beside the server, or switched in for it, with the name
-- the server it replaced was set aside under. A copy is a database of its own
-- once it has recovered, so its record outlives the archive it came from.
CREATE TABLE IF NOT EXISTS db_wal_restores (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  archive_id    INTEGER NOT NULL DEFAULT 0,
  source        TEXT NOT NULL,
  container     TEXT NOT NULL,
  connection_id INTEGER NOT NULL DEFAULT 0,
  port          INTEGER NOT NULL DEFAULT 0,
  target        TEXT NOT NULL DEFAULT '{}',
  base_backup   TEXT NOT NULL DEFAULT '',
  recovered_to  INTEGER NOT NULL DEFAULT 0,
  state         TEXT NOT NULL DEFAULT 'ready',
  replaced      TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  created_by    TEXT NOT NULL DEFAULT '',
  switched_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_db_wal_restores_archive ON db_wal_restores(archive_id, created_at DESC);

-- Boards live with the dashboard's other durable state, including embedded
-- Excalidraw image data. Revision guards prevent a stale tab overwriting a
-- newer save. The preview is derived from the scene on every save so the list
-- never reads a whole scene to draw its thumbnail; '' means a board saved
-- before previews existed, which the list fills in on first sight. A new table
-- is additive for existing installations, and preview reaches boards that
-- shipped without it through addedColumns.
CREATE TABLE IF NOT EXISTS boards (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL,
  scene      TEXT NOT NULL DEFAULT '{"elements":[],"appState":{},"files":{}}',
  preview    TEXT NOT NULL DEFAULT '',
  revision   INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_boards_updated ON boards(updated_at DESC, id DESC);

-- Excalidraw's shape library. A board has no owner, so the library is one per
-- install, the same for everyone who may draw; id is pinned to 1 so a second
-- row cannot exist. Revision guards a stale tab the way boards' does. Items
-- stay the JSON array the editor sent, because the server never reads inside
-- them. A new table is additive for existing installations.
CREATE TABLE IF NOT EXISTS board_library (
  id         INTEGER PRIMARY KEY CHECK (id = 1),
  items      TEXT NOT NULL DEFAULT '[]',
  revision   INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0
);

-- Images moved out of a board's scene, so a save does not carry every picture
-- again and a board's size is not its pictures'. The file id is Excalidraw's
-- own (a content hash, or random), so one id never names two different
-- contents on a board; a board's files go with the board. A new table is
-- additive for existing installations.
CREATE TABLE IF NOT EXISTS board_files (
  board_id   INTEGER NOT NULL,
  file_id    TEXT NOT NULL,
  mime_type  TEXT NOT NULL,
  data       BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (board_id, file_id)
);

-- Stickyboards, from 0.7.2 to 0.7.5. The feature is gone and nothing reads
-- this table, but it stays: schema changes are additive, and an install that
-- made boards keeps their documents rather than losing them on an update.
CREATE TABLE IF NOT EXISTS sticky_boards (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL,
  document   TEXT NOT NULL DEFAULT '{"version":1,"background":"dots","notes":[],"links":[]}',
  notes      INTEGER NOT NULL DEFAULT 0,
  links      INTEGER NOT NULL DEFAULT 0,
  preview    TEXT NOT NULL DEFAULT '{"notes":[],"links":[]}',
  revision   INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sticky_boards_updated ON sticky_boards(updated_at DESC, id DESC);

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
  recovery_json TEXT NOT NULL DEFAULT 'null',
  sqlite_paths TEXT NOT NULL DEFAULT '[]',
  database_dumps TEXT NOT NULL DEFAULT '[]',
  retention_days INTEGER NOT NULL DEFAULT 0,
  pause_containers TEXT NOT NULL DEFAULT '[]',
  revision    INTEGER NOT NULL DEFAULT 1,
  encrypt     INTEGER NOT NULL DEFAULT 0,
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
  trigger    TEXT NOT NULL DEFAULT 'manual',
  manifest_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_backup_runs_job ON backup_runs(job_id, started_at DESC);

CREATE TABLE IF NOT EXISTS backup_restore_tests (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id INTEGER NOT NULL REFERENCES backup_runs(id) ON DELETE CASCADE,
  state TEXT NOT NULL,
  owner_key TEXT NOT NULL,
  workspace TEXT NOT NULL,
  record_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_backup_restore_tests_run ON backup_restore_tests(run_id,id DESC);

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
  archived_at   INTEGER NOT NULL DEFAULT 0,
  archived_name TEXT NOT NULL DEFAULT ''
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
  run_number           INTEGER NOT NULL DEFAULT 0,
  source_revision      TEXT NOT NULL DEFAULT '',
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

CREATE TABLE IF NOT EXISTS github_app (
  id         INTEGER PRIMARY KEY CHECK (id = 1),
  app_id     INTEGER NOT NULL,
  slug       TEXT NOT NULL,
  name       TEXT NOT NULL,
  owner      TEXT NOT NULL DEFAULT '',
  html_url   TEXT NOT NULL DEFAULT '',
  client_id  TEXT NOT NULL DEFAULT '',
  secret_enc TEXT NOT NULL,
  created_at INTEGER NOT NULL
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
  rolled_back_at         INTEGER NOT NULL DEFAULT 0,
  rollback_reason        TEXT NOT NULL DEFAULT '',
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
	value_mode TEXT NOT NULL DEFAULT '',
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
  copied_from_environment INTEGER NOT NULL DEFAULT 0,
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

CREATE TABLE IF NOT EXISTS deploy_git_watches (
  environment_id INTEGER PRIMARY KEY REFERENCES deploy_environments(id) ON DELETE CASCADE,
  source_key     TEXT NOT NULL,
  revision       TEXT NOT NULL DEFAULT '',
  generation     INTEGER NOT NULL DEFAULT 0,
  run_id         INTEGER NOT NULL DEFAULT 0,
  status         TEXT NOT NULL DEFAULT 'watching',
  reason         TEXT NOT NULL DEFAULT '',
  policy_key     TEXT NOT NULL DEFAULT '',
  baseline_revision TEXT NOT NULL DEFAULT '',
  checked_at     INTEGER NOT NULL DEFAULT 0
);

-- Log drains: where deployments' output and requests are copied to. The
-- headers (a drain's credentials) are sealed in config_enc; the status row
-- is what the drain has shipped and how its last delivery went.
CREATE TABLE IF NOT EXISTS deploy_log_drains (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  name          TEXT NOT NULL UNIQUE,
  kind          TEXT NOT NULL,
  endpoint      TEXT NOT NULL,
  enabled       INTEGER NOT NULL DEFAULT 1,
  streams_json  TEXT NOT NULL DEFAULT '["output","requests"]',
  projects_json TEXT NOT NULL DEFAULT '[]',
  config_enc    TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS deploy_log_drain_status (
  drain_id        INTEGER PRIMARY KEY REFERENCES deploy_log_drains(id) ON DELETE CASCADE,
  shipped         INTEGER NOT NULL DEFAULT 0,
  dropped         INTEGER NOT NULL DEFAULT 0,
  batches         INTEGER NOT NULL DEFAULT 0,
  last_success_at INTEGER NOT NULL DEFAULT 0,
  last_error      TEXT NOT NULL DEFAULT '',
  last_error_at   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS deploy_git_policies (
  environment_id  INTEGER PRIMARY KEY REFERENCES deploy_environments(id) ON DELETE CASCADE,
  automatic       INTEGER NOT NULL DEFAULT 1,
  include_json    TEXT NOT NULL DEFAULT '[]',
  exclude_json    TEXT NOT NULL DEFAULT '[]',
  commit_statuses INTEGER NOT NULL DEFAULT 1,
  revision        INTEGER NOT NULL DEFAULT 1,
  updated_at      INTEGER NOT NULL DEFAULT 0,
  -- Production can refuse every build and take only staging's promotions;
  -- shipped without it through addedColumns.
  promotion_only  INTEGER NOT NULL DEFAULT 0
);

-- The last thing written back to the Git host for an environment: one row
-- for its commit status and one for its preview comment, so Settings can say
-- whether the host accepted it instead of leaving a refusal in the log.
CREATE TABLE IF NOT EXISTS deploy_forge_reports (
  environment_id INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  kind           TEXT NOT NULL,
  provider       TEXT NOT NULL DEFAULT '',
  repository     TEXT NOT NULL DEFAULT '',
  revision       TEXT NOT NULL DEFAULT '',
  state          TEXT NOT NULL DEFAULT '',
  run_id         INTEGER NOT NULL DEFAULT 0,
  ok             INTEGER NOT NULL DEFAULT 0,
  message        TEXT NOT NULL DEFAULT '',
  reported_at    INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (environment_id, kind)
);

CREATE TABLE IF NOT EXISTS deploy_database_networks (
  environment_id INTEGER PRIMARY KEY REFERENCES deploy_environments(id) ON DELETE CASCADE,
  network_name TEXT NOT NULL UNIQUE,
  network_id TEXT NOT NULL DEFAULT '',
  owner_key TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS deploy_database_bindings (
  environment_id INTEGER NOT NULL REFERENCES deploy_database_networks(environment_id) ON DELETE CASCADE,
  connection_id INTEGER NOT NULL REFERENCES db_connections(id) ON DELETE RESTRICT,
  container_name TEXT NOT NULL,
  compose_project TEXT NOT NULL DEFAULT '',
  compose_service TEXT NOT NULL DEFAULT '',
  container_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending',
  checked_at INTEGER NOT NULL DEFAULT 0,
  detail TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(environment_id, connection_id)
);

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
  -- The deploy key that made the draft, or 0 for a session: a key's drafts
  -- are its own, and it reaches no other.
  key_id               INTEGER NOT NULL DEFAULT 0,
  current_step         TEXT NOT NULL DEFAULT 'intent',
  revision             INTEGER NOT NULL DEFAULT 1,
  data_json            TEXT NOT NULL DEFAULT '{}',
  environment_enc      TEXT NOT NULL DEFAULT '',
  adoption_enc         TEXT NOT NULL DEFAULT '',
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
  kind        TEXT NOT NULL DEFAULT 'webhook',
  url         TEXT NOT NULL,
  target      TEXT NOT NULL DEFAULT '',
  headers_enc TEXT NOT NULL DEFAULT '',
  secret_enc  TEXT NOT NULL DEFAULT '',
  config_enc  TEXT NOT NULL DEFAULT '',
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

CREATE TABLE IF NOT EXISTS deploy_traffic_alerts (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id     INTEGER NOT NULL REFERENCES deploy_projects(id) ON DELETE CASCADE,
  environment_id INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  kind           TEXT NOT NULL,
  threshold      REAL NOT NULL DEFAULT 0,
  window_minutes INTEGER NOT NULL DEFAULT 5,
  channels       TEXT NOT NULL DEFAULT '[]',
  enabled        INTEGER NOT NULL DEFAULT 1,
  state          TEXT NOT NULL DEFAULT 'ok',
  state_since    INTEGER NOT NULL DEFAULT 0,
  observed       REAL NOT NULL DEFAULT 0,
  checked_at     INTEGER NOT NULL DEFAULT 0,
  fired_at       INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_deploy_traffic_alerts_project ON deploy_traffic_alerts(project_id);

CREATE TABLE IF NOT EXISTS deploy_preview_refs (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  trigger_id     INTEGER NOT NULL REFERENCES deploy_triggers(id) ON DELETE CASCADE,
  provider_ref   TEXT NOT NULL,
  environment_id INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  state          TEXT NOT NULL DEFAULT 'open',
  updated_at     INTEGER NOT NULL,
  origin         TEXT NOT NULL DEFAULT '',
  title          TEXT NOT NULL DEFAULT '',
  head_revision  TEXT NOT NULL DEFAULT '',
  variables_copied_revision TEXT NOT NULL DEFAULT '',
  UNIQUE(trigger_id, provider_ref)
);


CREATE TABLE IF NOT EXISTS deploy_preview_approvals (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  trigger_id   INTEGER NOT NULL REFERENCES deploy_triggers(id) ON DELETE CASCADE,
  provider_ref TEXT NOT NULL,
  revision     TEXT NOT NULL,
  event_json   TEXT NOT NULL,
  state        TEXT NOT NULL DEFAULT 'pending',
  approved_by  TEXT NOT NULL DEFAULT '',
  generation   INTEGER NOT NULL DEFAULT 1,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL,
  UNIQUE(trigger_id, provider_ref, revision)
);

CREATE TABLE IF NOT EXISTS deploy_preview_quarantines (
  environment_id INTEGER PRIMARY KEY REFERENCES deploy_environments(id) ON DELETE CASCADE,
  status         TEXT NOT NULL DEFAULT 'pending',
  reason         TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);

-- Where a preview answers: a port on this host's Tailscale node that
-- tailscale serve maps to the preview's loopback publication. The port is
-- unique per kind so two previews can never be served on the same one.

CREATE TABLE IF NOT EXISTS deploy_preview_addresses (
  environment_id INTEGER PRIMARY KEY REFERENCES deploy_environments(id) ON DELETE CASCADE,
  kind           TEXT NOT NULL DEFAULT 'tailnet',
  port           INTEGER NOT NULL,
  upstream_port  INTEGER NOT NULL DEFAULT 0,
  url            TEXT NOT NULL DEFAULT '',
  published      INTEGER NOT NULL DEFAULT 0,
  updated_at     INTEGER NOT NULL,
  UNIQUE(kind, port)
);


CREATE TABLE IF NOT EXISTS watched_domains (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  domain     TEXT NOT NULL UNIQUE,
  port       INTEGER NOT NULL DEFAULT 443,
  created_at INTEGER NOT NULL
);

-- Watched endpoints. watched_domains holds one row per name, so watching
-- mail.example.com on 993 replaced it on 443. An endpoint is a name, a port
-- and an address to reach the name at ('' for the name's own), and keeps its
-- last live check, which the server makes on its own schedule. kind is what
-- the check asks: 'tls', a handshake and its certificate (kept in
-- certificate), or 'tcp', a network probe that only connects (kept in
-- probe) — the network page's watched probes, on the same schedule, history
-- and alerts. Here rather than in proxySchema because it gained columns after
-- shipping, which addedColumns brings to an older table first.
CREATE TABLE IF NOT EXISTS watched_endpoints (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  domain      TEXT NOT NULL,
  port        INTEGER NOT NULL DEFAULT 443,
  ip          TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  checked_at  INTEGER NOT NULL DEFAULT 0,
  certificate TEXT NOT NULL DEFAULT '',
  kind        TEXT NOT NULL DEFAULT 'tls',
  probe       TEXT NOT NULL DEFAULT '',
  UNIQUE(domain, port, ip)
);

-- The watch list as watched_domains had it. This runs on every boot and
-- brings back nothing removed since, because removing an endpoint removes
-- its watched_domains row as well; watched_domains is otherwise left as it
-- was, for a downgrade to find.
INSERT OR IGNORE INTO watched_endpoints(domain, port, ip, created_at)
  SELECT domain, port, '', created_at FROM watched_domains;

-- Every check of a watched endpoint, by the schedule or an administrator,
-- so a row can show how its days left, or a probe's connect time, moved and
-- when its certificate changed. Kept to 2000 per endpoint and 90 days.
CREATE TABLE IF NOT EXISTS watched_checks (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  endpoint_id INTEGER NOT NULL REFERENCES watched_endpoints(id) ON DELETE CASCADE,
  checked_at  INTEGER NOT NULL,
  days_left   INTEGER,
  fingerprint TEXT NOT NULL DEFAULT '',
  error       TEXT NOT NULL DEFAULT '',
  ms          INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_watched_checks_endpoint ON watched_checks(endpoint_id, checked_at);

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

-- Named volumes' sizes, recorded by the same sampler at a slower pace
-- (metrics.VolumeInterval) and only for the volumes a deployment owns.
--
-- A volume's size is a point on a line, not a reading: "the uploads volume is
-- 38 GB" is the size of the thing, and only its growth since last week says
-- whether the disk under it has a month left or a day. Docker's disk walk is
-- what measures a volume, which is why this is sampled every quarter of an
-- hour and not every fifteen seconds. Pruned past JD_METRICS_RETENTION.
CREATE TABLE IF NOT EXISTS metric_volume_samples (
  ts         INTEGER NOT NULL,
  name       TEXT NOT NULL,
  size_bytes INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (name, ts)
);
CREATE INDEX IF NOT EXISTS idx_volume_samples_ts ON metric_volume_samples(ts);

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

-- Per-device traffic, recorded by the network module's sampler at the metrics
-- interval (netx/sampler.go). Each row is the interval's mean and its busiest
-- two seconds in each direction, and the errors and drops counted in it.
-- Docker's veths are left out: each container's traffic is recorded with the
-- container, and a row per veth per interval would be most of the table.
CREATE TABLE IF NOT EXISTS metric_interface_samples (
  ts         INTEGER NOT NULL,
  iface      TEXT NOT NULL,
  rx_rate    REAL NOT NULL DEFAULT 0,
  tx_rate    REAL NOT NULL DEFAULT 0,
  rx_peak    REAL NOT NULL DEFAULT 0,
  tx_peak    REAL NOT NULL DEFAULT 0,
  rx_errors  INTEGER NOT NULL DEFAULT 0,
  tx_errors  INTEGER NOT NULL DEFAULT 0,
  rx_dropped INTEGER NOT NULL DEFAULT 0,
  tx_dropped INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (iface, ts)
);
CREATE INDEX IF NOT EXISTS idx_interface_samples_ts ON metric_interface_samples(ts);

-- Bounded saved observations from the existing network probe dispatcher.
CREATE TABLE IF NOT EXISTS network_diagnostic_runs (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  job_id     TEXT NOT NULL DEFAULT '',
  status     TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  payload    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_network_diagnostic_runs_created ON network_diagnostic_runs(created_at DESC, id);

-- Explicit bounded PCAP artifacts; private API access and fixed retention.
CREATE TABLE IF NOT EXISTS network_packet_captures (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  payload TEXT NOT NULL CHECK(length(payload)<=65536),
  artifact BLOB NOT NULL DEFAULT X'' CHECK(length(artifact)<=2097152)
);
CREATE INDEX IF NOT EXISTS idx_network_packet_captures_created ON network_packet_captures(created_at DESC,id);
-- Opt-in socket snapshots retain only measured TCP deltas and peer metadata.
-- Payload bytes, rows, UTC-hour coverage and retention are capped by netflows.
CREATE TABLE IF NOT EXISTS network_flow_buckets (
  id TEXT NOT NULL,
  hour INTEGER NOT NULL,
  last_seen INTEGER NOT NULL,
  remote_address TEXT NOT NULL DEFAULT '',
  container_id TEXT NOT NULL DEFAULT '',
  payload TEXT NOT NULL,
  payload_bytes INTEGER NOT NULL,
  PRIMARY KEY (id, hour)
);
CREATE INDEX IF NOT EXISTS idx_network_flow_hour ON network_flow_buckets(hour, last_seen);
CREATE INDEX IF NOT EXISTS idx_network_flow_peer ON network_flow_buckets(remote_address, hour);
CREATE INDEX IF NOT EXISTS idx_network_flow_container ON network_flow_buckets(container_id, hour);
CREATE TABLE IF NOT EXISTS network_flow_cycles (
  hour INTEGER PRIMARY KEY,
  payload TEXT NOT NULL,
  payload_bytes INTEGER NOT NULL
);

-- A WireGuard client's configuration, kept sealed (auth.Sealer) so an
-- administrator can show its QR code again after the sheet that made it was
-- closed. The client's private key is in it, which is why it is sealed and
-- why it can be forgotten (config_sealed emptied) while the peer stays. The
-- peer itself lives in the interface's own wg-quick file; this is only the
-- half the server never needs.
CREATE TABLE IF NOT EXISTS network_vpn_clients (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  iface         TEXT NOT NULL,
  public_key    TEXT NOT NULL,
  name          TEXT NOT NULL,
  kind          TEXT NOT NULL DEFAULT 'device',
  address       TEXT NOT NULL DEFAULT '',
  config_sealed TEXT NOT NULL DEFAULT '',
  created_by    TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  UNIQUE (iface, public_key)
);

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

-- What a compose stack looked like before somebody changed it.
--
-- Docker keeps no history. Bringing a compose project up replaces what was
-- running, and the previous configuration is gone unless it happened to be
-- committed -- so "what changed" and "put it back" are questions nothing on
-- the server can answer. This is the smallest record that makes both
-- answerable: the compose file as it was, the image digests that were
-- actually running, and who did it.
--
-- Deliberately not one of the deploy_* tables above. Those describe this
-- dashboard's own deployment pipeline, with projects, environments and
-- releases; this describes a compose stack somebody brought up by hand, which
-- most stacks on most servers are. Rows are append-only and pruned by count
-- per project.
CREATE TABLE IF NOT EXISTS docker_stack_deployments (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  project       TEXT NOT NULL,
  working_dir   TEXT NOT NULL DEFAULT '',
  config_hash   TEXT NOT NULL DEFAULT '',
  config        TEXT NOT NULL DEFAULT '',
  services      TEXT NOT NULL DEFAULT '[]',
  image_digests TEXT NOT NULL DEFAULT '{}',
  env_hash      TEXT NOT NULL DEFAULT '',
  git_commit    TEXT NOT NULL DEFAULT '',
  git_branch    TEXT NOT NULL DEFAULT '',
  git_dirty     INTEGER NOT NULL DEFAULT 0,
  actor         TEXT NOT NULL DEFAULT '',
  source        TEXT NOT NULL DEFAULT '',
  action        TEXT NOT NULL DEFAULT '',
  result        TEXT NOT NULL DEFAULT '',
  detail        TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stack_deployments_project
  ON docker_stack_deployments(project, created_at DESC);
`

// Indexes that name added columns must run after applyAddedColumns. Putting
// them in schema makes an upgrade fail before the ALTERs can add those columns
// to a 0.6.6 deploy_runs table; a fresh database reaches the same final shape
// through this second block.
const postColumnSchema = `
CREATE TRIGGER IF NOT EXISTS deploy_run_source_revision_immutable
BEFORE UPDATE OF source_revision ON deploy_runs
BEGIN
  SELECT RAISE(ABORT, 'deployment run source revision is immutable');
END;
WITH numbered AS MATERIALIZED (
  SELECT id, ROW_NUMBER() OVER (PARTITION BY project_id ORDER BY id) AS number
  FROM deploy_runs
)
UPDATE deploy_runs SET run_number = (SELECT number FROM numbered WHERE numbered.id = deploy_runs.id)
WHERE run_number = 0;
CREATE UNIQUE INDEX IF NOT EXISTS idx_deploy_runs_project_number
  ON deploy_runs(project_id, run_number) WHERE run_number > 0;
-- Both legacy and persistent-engine inserts allocate inside the same SQLite write.
CREATE TRIGGER IF NOT EXISTS deploy_run_number_allocate
AFTER INSERT ON deploy_runs WHEN NEW.run_number = 0
BEGIN
  UPDATE deploy_runs SET run_number = (
    SELECT COALESCE(MAX(run_number), 0) + 1 FROM deploy_runs
    WHERE project_id = NEW.project_id AND run_number > 0
  ) WHERE id = NEW.id;
END;
CREATE INDEX IF NOT EXISTS idx_container_samples_identity_ts
  ON metric_container_samples(container_id, ts);
CREATE INDEX IF NOT EXISTS idx_deploy_runs_queue
  ON deploy_runs(state, priority, requested_at, id);
-- Every fleet poll ranks each project's runs newest first; an index in that
-- order lets the ranking read ids from the index alone instead of sorting rows.
CREATE INDEX IF NOT EXISTS idx_deploy_runs_project_requested
  ON deploy_runs(project_id, requested_at DESC, id DESC);
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
CREATE TRIGGER IF NOT EXISTS deploy_variable_revision_mode_immutable
BEFORE UPDATE OF value_mode ON deploy_variable_revisions
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
	// Access completion predates factor provenance; old sessions cannot prove a method.
	{"sessions", "auth_method", "TEXT NOT NULL DEFAULT 'unknown'"},
	{"users", "totp_last_step", "INTEGER NOT NULL DEFAULT -1"},
	// An account gained a name to show and a picture beside it. The sign-in
	// name stays the lower-cased key it always was; the display name is what
	// the operator typed, case and all.
	{"users", "display_name", "TEXT NOT NULL DEFAULT ''"},
	{"users", "avatar", "BLOB NOT NULL DEFAULT x''"},
	{"users", "avatar_type", "TEXT NOT NULL DEFAULT ''"},
	{"users", "avatar_at", "INTEGER NOT NULL DEFAULT 0"},
	// A new account is asked what it uses the dashboard for and keeps the
	// sections it does not need out of its sidebar. The default is the answer
	// for every account that existed before the question did: already past
	// it, with nothing hidden, so an upgrade changes nobody's rail.
	{"users", "needs_onboarding", "INTEGER NOT NULL DEFAULT 0"},
	{"users", "hidden_sections", "TEXT NOT NULL DEFAULT '[]'"},
	{"backup_runs", "manifest_json", "TEXT NOT NULL DEFAULT '{}'"},
	{"backup_jobs", "recovery_json", "TEXT NOT NULL DEFAULT 'null'"},
	{"backup_jobs", "sqlite_paths", "TEXT NOT NULL DEFAULT '[]'"},
	// Backup jobs learned to capture native database dumps beside their files.
	{"backup_jobs", "database_dumps", "TEXT NOT NULL DEFAULT '[]'"},
	// Backup jobs learned to prune by age and to freeze containers while
	// they archive the volumes those containers write to.
	{"backup_jobs", "retention_days", "INTEGER NOT NULL DEFAULT 0"},
	{"backup_jobs", "pause_containers", "TEXT NOT NULL DEFAULT '[]'"},
	{"backup_jobs", "revision", "INTEGER NOT NULL DEFAULT 1"},
	// Backup jobs learned to encrypt their archives. A job that predates the
	// setting keeps writing what it always wrote until somebody turns it on.
	{"backup_jobs", "encrypt", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_preview_approvals", "generation", "INTEGER NOT NULL DEFAULT 1"},
	// A preview started from the dashboard's "Test this pull request" rather
	// than from a webhook remembers where it came from, the pull request's
	// title, the newest head the reconciler has seen, and the head whose
	// production variables were copied in ('' = none copied).
	{"deploy_preview_refs", "origin", "TEXT NOT NULL DEFAULT ''"},
	{"deploy_preview_refs", "title", "TEXT NOT NULL DEFAULT ''"},
	{"deploy_preview_refs", "head_revision", "TEXT NOT NULL DEFAULT ''"},
	{"deploy_preview_refs", "variables_copied_revision", "TEXT NOT NULL DEFAULT ''"},
	// A variable copied from production into a preview keeps its provenance,
	// so closing the preview can drop exactly the copies and nothing the
	// operator typed there themselves.
	{"deploy_variable_revisions", "copied_from_environment", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_variable_revisions", "value_mode", "TEXT NOT NULL DEFAULT ''"},

	{"deploy_git_watches", "reason", "TEXT NOT NULL DEFAULT ''"},
	{"deploy_git_watches", "policy_key", "TEXT NOT NULL DEFAULT ''"},
	{"deploy_git_watches", "baseline_revision", "TEXT NOT NULL DEFAULT ''"},
	// A connection remembers which discovered database it was made from, by
	// the inventory's key. Where it dials can change — a container is given a
	// new address when it is recreated — and the key does not.
	{"db_connections", "origin", "TEXT NOT NULL DEFAULT ''"},
	{"deploy_database_networks", "network_id", "TEXT NOT NULL DEFAULT ''"},
	// A saved connection gained what the operator says about it: the
	// environment it serves, a protection that refuses every write made
	// through the dashboard, and free notes. Existing connections are
	// unlabelled, unprotected and have none.
	{"db_connections", "environment", "TEXT NOT NULL DEFAULT ''"},
	{"db_connections", "read_only", "INTEGER NOT NULL DEFAULT 0"},
	{"db_connections", "notes", "TEXT NOT NULL DEFAULT ''"},
	// Reconciliation knew why a binding could not be repaired and threw the
	// reason away, so the settings page could only say "check that the
	// container is running". The reason is kept on the binding it belongs to.
	{"deploy_database_bindings", "detail", "TEXT NOT NULL DEFAULT ''"},
	// Notification channels grew provider kinds beside the signed webhook.
	// Existing rows are webhooks whose URL is also their display target.
	{"deploy_notification_channels", "kind", "TEXT NOT NULL DEFAULT 'webhook'"},
	{"deploy_notification_channels", "target", "TEXT NOT NULL DEFAULT ''"},
	{"deploy_notification_channels", "config_enc", "TEXT NOT NULL DEFAULT ''"},
	// Commit statuses default on: an environment that already deploys from
	// GitHub gains them without an edit, and the policy editor can turn them off.
	{"deploy_git_policies", "commit_statuses", "INTEGER NOT NULL DEFAULT 1"},
	// Production may take only promotions from staging. Every policy saved
	// before builds as it always did.
	{"deploy_git_policies", "promotion_only", "INTEGER NOT NULL DEFAULT 0"},
	// 0.6.7 keeps the shipped project/run rows as stable compatibility
	// identities while normalized environments, releases, steps and events
	// grow beside them. Every legacy row receives a usable zero/default before
	// the transactional mapper fills its production environment.
	{"deploy_projects", "profile", "TEXT NOT NULL DEFAULT 'compose'"},
	{"deploy_projects", "updated_at", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_projects", "archived_at", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_projects", "archived_name", "TEXT NOT NULL DEFAULT ''"},
	{"deploy_drafts", "environment_enc", "TEXT NOT NULL DEFAULT ''"},
	{"deploy_drafts", "adoption_enc", "TEXT NOT NULL DEFAULT ''"},
	{"deploy_drafts", "key_id", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_runs", "environment_id", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_runs", "run_number", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_runs", "source_revision", "TEXT NOT NULL DEFAULT ''"},
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

	// The query history learned why a statement failed and how many rows a
	// write changed, and a saved query learned when it was last edited.
	{"db_query_history", "error", "TEXT NOT NULL DEFAULT ''"},
	{"db_query_history", "rows_affected", "INTEGER NOT NULL DEFAULT 0"},
	{"db_saved_queries", "updated_at", "INTEGER NOT NULL DEFAULT 0"},

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
	// The writable layer, sampled alongside everything else. A static figure
	// cannot tell a container that has held 38 GB for six months from one that
	// gained 12 GB today, and only the second is a disk about to fill.
	{"metric_container_samples", "size_rw", "INTEGER NOT NULL DEFAULT 0"},
	// Name continuity serves Docker charts; release attribution needs the exact
	// observed container identity. Old samples deliberately remain unattributed.
	{"metric_container_samples", "container_id", "TEXT NOT NULL DEFAULT ''"},
	// Unknown for pre-upgrade rows: an absent measurement must not become an
	// idle interface or an operator-configured limit during migration.
	{"metric_container_samples", "network_available", "INTEGER DEFAULT NULL"},
	{"metric_container_samples", "block_available", "INTEGER DEFAULT NULL"},
	{"metric_container_samples", "mem_limited", "INTEGER DEFAULT NULL"},
	{"metric_container_samples", "cpu_total", "INTEGER DEFAULT NULL"},
	{"metric_container_samples", "sample_time", "REAL DEFAULT NULL"},
	// The CFS scheduler's cumulative periods and the periods it throttled the
	// container in, NULL for a container without a CPU limit (it has no
	// periods) and for every row from before they were kept: throttling is
	// what a CPU limit set too low does, and a mean CPU figure under the limit
	// hides it entirely.
	{"metric_container_samples", "cpu_periods", "INTEGER DEFAULT NULL"},
	{"metric_container_samples", "cpu_throttled", "INTEGER DEFAULT NULL"},

	// Inode exhaustion fills a filesystem that reports free space, and is
	// invisible in a used-bytes percentage.
	{"metric_mount_samples", "inodes_percent", "REAL NOT NULL DEFAULT 0"},

	// A device's own routes (its AllowedIPs) live only in its sealed client
	// configuration; this unsealed copy lets an edit start from them. Empty for
	// a peer made before it existed, which reads as unknown, never as none.
	{"network_vpn_clients", "client_routes", "TEXT NOT NULL DEFAULT ''"},

	// A device's recorded interval carries its exact counter growth and the
	// seconds it covered, so a transfer budget is measured rather than inferred
	// from a mean rate. Unknown (NULL) for rows recorded before, never zero.
	{"metric_interface_samples", "rx_bytes", "INTEGER DEFAULT NULL"},
	{"metric_interface_samples", "tx_bytes", "INTEGER DEFAULT NULL"},
	{"metric_interface_samples", "rx_packets", "INTEGER DEFAULT NULL"},
	{"metric_interface_samples", "tx_packets", "INTEGER DEFAULT NULL"},
	{"metric_interface_samples", "span", "INTEGER DEFAULT NULL"},
	// A watched endpoint may be a network probe that only connects, checked
	// on the TLS watch's schedule with the same history and alerts, rather
	// than a handshake; a probe's check keeps its connect time.
	{"watched_endpoints", "kind", "TEXT NOT NULL DEFAULT 'tls'"},
	{"watched_endpoints", "probe", "TEXT NOT NULL DEFAULT ''"},
	{"watched_checks", "ms", "INTEGER NOT NULL DEFAULT 0"},
	// How connections fared, not only how many there were: TCP's own rates of
	// resent segments, failed attempts, resets and accept-queue drops, and the
	// kernel's RTT of established connections to peers elsewhere. NULL on a
	// row from before they were sampled, so an old hour reads as unmeasured
	// rather than as a clean one.
	{"metric_samples", "tcp_out_segs", "REAL DEFAULT NULL"},
	{"metric_samples", "tcp_retrans", "REAL DEFAULT NULL"},
	{"metric_samples", "tcp_attempt_fails", "REAL DEFAULT NULL"},
	{"metric_samples", "tcp_estab_resets", "REAL DEFAULT NULL"},
	{"metric_samples", "tcp_listen_drops", "REAL DEFAULT NULL"},
	{"metric_samples", "tcp_rtt_ms", "REAL DEFAULT NULL"},
	{"metric_samples", "tcp_rtt_p90_ms", "REAL DEFAULT NULL"},
	// A board gained a thumbnail derived from its scene on every save. Boards
	// saved before it hold '' until the list first sees them.
	{"boards", "preview", "TEXT NOT NULL DEFAULT ''"},
	// A computer paired with Remote Desktop's stream bridge keeps the
	// bridge's id for it; 0 is a computer that streams nothing yet.
	{"remote_desktop_computers", "stream_host_id", "INTEGER NOT NULL DEFAULT 0"},
	{"remote_desktop_computers", "gfx", "INTEGER NOT NULL DEFAULT 0"},
	// An API key can be narrowed to one capability, such as reading the
	// exported database metrics. Every key that existed before is unscoped.
	{"api_tokens", "scope", "TEXT NOT NULL DEFAULT ''"},
	// An MCP key can be given the deployments as well as databases. Every
	// key made before reaches none of them.
	{"api_tokens", "deploy_access", "TEXT NOT NULL DEFAULT ''"},
	// A release taken back by an instant rollback says when and why: the
	// reading that decided it, or the operator who pressed it. Every release
	// made before was never rolled back this way.
	{"deploy_releases", "rolled_back_at", "INTEGER NOT NULL DEFAULT 0"},
	{"deploy_releases", "rollback_reason", "TEXT NOT NULL DEFAULT ''"},
	// The audit trail names the API key behind a request. Rows written
	// before it name only the account, as they always did.
	{"audit_log", "key_id", "INTEGER NOT NULL DEFAULT 0"},
	{"audit_log", "key_name", "TEXT NOT NULL DEFAULT ''"},
	// A drain that exports traces may name where its service shows one, a
	// link the request console draws beside a request's trace id. Drains
	// made before had no traces to link to.
	{"deploy_log_drains", "trace_url", "TEXT NOT NULL DEFAULT ''"},
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
	// Transactions begin IMMEDIATE: a transaction that reads first and writes
	// later under a deferred BEGIN fails at once with SQLITE_BUSY_SNAPSHOT
	// when any other connection commits in between, and the busy timeout never
	// applies to that failure. Taking the write lock up front makes the second
	// writer wait instead. A deployment worker's step transition racing a
	// notification delivery or an operator's settings save is exactly that
	// case, and a lost transition used to end the run as unrecoverable.
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate")
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
	if err := InitializeNetworkDNSEvidence(context.Background(), db); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply network DNS evidence schema: %w", err)
	}
	if err := InitializeNetworkDNSServices(context.Background(), db); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply managed DNS service schema: %w", err)
	}
	// Run after the schema, never instead of it: a fresh database gets its
	// tables from the block above and finds nothing to add, while an existing
	// one gets only the columns it is missing.
	if err := applyAddedColumns(context.Background(), db); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(context.Background(), proxySchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply proxy schema: %w", err)
	}
	// Retain historical names separately while releasing the unique live name.
	// This also repairs projects archived before name reuse was supported.
	if _, err := db.ExecContext(context.Background(), `UPDATE deploy_projects
		SET archived_name = name, name = '__jd_archived_' || id || '_' || lower(hex(randomblob(16)))
		WHERE archived_at > 0 AND archived_name = ''`); err != nil {
		db.Close()
		return nil, fmt.Errorf("release archived deployment names: %w", err)
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
	guard, err := holdWALGuard(path)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db, guard: guard}, nil
}

func (s *Store) Close() error {
	s.guard.release()
	return s.DB.Close()
}

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

// SetSettings keeps related settings visible together when one request changes both.
func (s *Store) SetSettings(ctx context.Context, values map[string]string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range values {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			key, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}
