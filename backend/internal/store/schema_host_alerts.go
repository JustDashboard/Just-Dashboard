package store

// hostAlertSchema holds the host alerts' tables (api/host_alerts.go): rules
// over the machine's own readings — CPU and memory held high, a filesystem
// filling, a container gone down, a backup job failed — that tell the
// deployments' notification channels. Kept apart from `schema` so the block is
// read in one place; it is concatenated into `schema`, so the same rules hold:
// CREATE ... IF NOT EXISTS only, and a column one of these tables gains after
// it has shipped goes into addedColumns.
const hostAlertSchema = `
-- A rule is a kind with its parameters (a line, minutes) and the channels it
-- tells; channels = '[]' tells every enabled one. revision moves on every
-- edit, so a pass that read the old terms never writes its verdict over the
-- new ones. watch_from is when the rule began watching on its current terms:
-- a container that stopped before it is not one this rule saw go down.
CREATE TABLE IF NOT EXISTS host_alert_rules (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  kind       TEXT NOT NULL,
  params     TEXT NOT NULL DEFAULT '{}',
  channels   TEXT NOT NULL DEFAULT '[]',
  enabled    INTEGER NOT NULL DEFAULT 1,
  revision   INTEGER NOT NULL DEFAULT 1,
  watch_from INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
-- One subject a rule follows — "cpu", "memory", a mountpoint, a container's
-- name, "job:<id>" — while it is firing, holding before it is told, or muted.
-- A quiet subject nobody muted has no row. firing_since is when the reading
-- first found it over the line, seen the passes in a row that did, and
-- notified_at when its firing was told (0 while it holds).
CREATE TABLE IF NOT EXISTS host_alert_state (
  rule_id      INTEGER NOT NULL REFERENCES host_alert_rules(id) ON DELETE CASCADE,
  subject      TEXT NOT NULL,
  label        TEXT NOT NULL DEFAULT '',
  detail       TEXT NOT NULL DEFAULT '',
  seen         INTEGER NOT NULL DEFAULT 0,
  firing_since INTEGER NOT NULL DEFAULT 0,
  notified_at  INTEGER NOT NULL DEFAULT 0,
  muted        INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (rule_id, subject)
);
-- What each pass told, or kept back for a mute, and what each test sent, for
-- the Alerts page's history. The rule may since be gone; the newest 500 are
-- kept.
CREATE TABLE IF NOT EXISTS host_alert_events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  rule_id    INTEGER NOT NULL,
  kind       TEXT NOT NULL,
  subject    TEXT NOT NULL,
  label      TEXT NOT NULL DEFAULT '',
  event      TEXT NOT NULL,
  detail     TEXT NOT NULL DEFAULT '',
  delivered  INTEGER NOT NULL DEFAULT 0,
  failed     INTEGER NOT NULL DEFAULT 0,
  muted      INTEGER NOT NULL DEFAULT 0,
  test       INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
`
