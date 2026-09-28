package store

// proxySchema holds the proxy features' own tables, apart from `schema` so
// each area of the proxy pages adds its tables under its own heading without
// touching the block every other feature shares.
//
// Open runs it after applyAddedColumns, so an index here may name a column
// that addedColumns brings to an older table (watched_domains, say). The same
// rules hold as for `schema`: CREATE ... IF NOT EXISTS only, nothing dropped or
// renamed. A table created here is created whole; a column it gains after it
// has shipped goes into addedColumns, and because that runs first, such a table
// has to move into `schema` in the same change or a fresh install fails to add
// a column to a table that does not exist yet.
const proxySchema = `
-- --- lane A: engine & insights ---

-- Every configuration file the proxy service changed, as it was after the
-- change, and as it was before whenever that differs from the last revision
-- kept: the file was changed outside the dashboard in between. existed = 0
-- is a file the change removed. The recorder keeps the newest 50 per path.
CREATE TABLE IF NOT EXISTS proxy_config_revisions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    path       TEXT NOT NULL,
    sha256     TEXT NOT NULL,
    content    BLOB NOT NULL,
    existed    INTEGER NOT NULL DEFAULT 1,
    action     TEXT NOT NULL,
    actor      TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_proxy_config_revisions_path ON proxy_config_revisions(path, created_at);

-- Proxy alerts. A rule is a kind (certificate expiring, engine down, ...)
-- with its parameters and the notification channels it tells; channels = '[]'
-- tells every enabled one. A state row is one subject the rule watches — a
-- certificate's path, the engine's unit, an upstream, a watched endpoint, a
-- site — at one level (a certificate crosses 21, then 7 days): firing_since
-- is 0 while the subject is fine, notified_at is when its firing was told and
-- recovered_at when its recovery was. seen counts the passes in a row that
-- found it firing, for the kinds that must hold before they are told. muted
-- keeps a subject's messages back while its state is still followed.
CREATE TABLE IF NOT EXISTS proxy_alert_rules (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT NOT NULL,
    params     TEXT NOT NULL DEFAULT '{}',
    channels   TEXT NOT NULL DEFAULT '[]',
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS proxy_alert_state (
    rule_id      INTEGER NOT NULL,
    subject      TEXT NOT NULL,
    level        TEXT NOT NULL,
    label        TEXT NOT NULL DEFAULT '',
    detail       TEXT NOT NULL DEFAULT '',
    seen         INTEGER NOT NULL DEFAULT 0,
    firing_since INTEGER NOT NULL DEFAULT 0,
    notified_at  INTEGER NOT NULL DEFAULT 0,
    recovered_at INTEGER NOT NULL DEFAULT 0,
    muted        INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (rule_id, subject, level)
);
-- What each pass told, or kept back, for the Alerts sheet's history. The
-- newest 500 are kept.
CREATE TABLE IF NOT EXISTS proxy_alert_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    rule_id    INTEGER NOT NULL,
    kind       TEXT NOT NULL,
    subject    TEXT NOT NULL,
    level      TEXT NOT NULL,
    label      TEXT NOT NULL DEFAULT '',
    event      TEXT NOT NULL,
    detail     TEXT NOT NULL DEFAULT '',
    delivered  INTEGER NOT NULL DEFAULT 0,
    failed     INTEGER NOT NULL DEFAULT 0,
    muted      INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

-- A finding the operator put aside on the overview. The findings are judged
-- in the browser, so the row keeps the fingerprint the finding had when it
-- was snoozed: one that reads differently now is shown again. until = 0 is
-- "until it changes"; otherwise the row counts only until that Unix second.
CREATE TABLE IF NOT EXISTS proxy_finding_snoozes (
    finding_id  TEXT PRIMARY KEY,
    fingerprint TEXT NOT NULL,
    until       INTEGER NOT NULL DEFAULT 0,
    note        TEXT NOT NULL DEFAULT '',
    actor       TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);

-- --- lane B: sites list & lifecycle ---

-- --- lane C: site builder ---

-- --- lane D: streams ---

-- --- lane E: ports & exposure ---

-- --- lane F: certificates ---

-- --- lane G: TLS report & monitoring ---
`
