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

-- --- lane B: sites list & lifecycle ---

-- --- lane C: site builder ---

-- --- lane D: streams ---

-- --- lane E: ports & exposure ---

-- The ports history (proxysvc/ports_history.go): each stretch of time one
-- listening socket was seen with one owner, sampled once a minute. A socket
-- opened between opened_after and first_seen — opened_after is NULL for one
-- already listening when recording began, whose opening nobody saw — and
-- closed between gone_after and gone_at, both NULL while it listens. Times are
-- Unix seconds. Stretches that closed more than thirty days ago are pruned.
CREATE TABLE IF NOT EXISTS listener_observations (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  protocol     TEXT NOT NULL,
  family       TEXT NOT NULL,
  address      TEXT NOT NULL,
  port         INTEGER NOT NULL,
  process      TEXT NOT NULL DEFAULT '',
  username     TEXT NOT NULL DEFAULT '',
  pid          INTEGER NOT NULL DEFAULT 0,
  cmdline      TEXT NOT NULL DEFAULT '',
  opened_after INTEGER,
  first_seen   INTEGER NOT NULL,
  gone_after   INTEGER,
  gone_at      INTEGER
);
-- One socket listens once: a second open stretch for it would double every
-- event after it, so the sampler's write fails instead.
CREATE UNIQUE INDEX IF NOT EXISTS listener_observations_open
  ON listener_observations(protocol, family, address, port) WHERE gone_at IS NULL;
CREATE INDEX IF NOT EXISTS listener_observations_first_seen ON listener_observations(first_seen);
CREATE INDEX IF NOT EXISTS listener_observations_gone_at ON listener_observations(gone_at);

-- When the ports history began and its latest sample, one row: the sample a
-- change is dated after, across a restart as much as across a minute.
CREATE TABLE IF NOT EXISTS listener_history (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  started_at  INTEGER NOT NULL,
  last_sample INTEGER NOT NULL
);

-- --- lane F: certificates ---

-- --- lane G: TLS report & monitoring ---

-- Watched TLS endpoints. watched_domains holds one row per name, so watching
-- mail.example.com on 993 replaced it on 443. An endpoint is a name, a port
-- and an address to reach the name at ('' for the name's own), and keeps its
-- last live check: only an administrator's visit runs one, and everyone else
-- reads what it found and when.
CREATE TABLE IF NOT EXISTS watched_endpoints (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  domain      TEXT NOT NULL,
  port        INTEGER NOT NULL DEFAULT 443,
  ip          TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  checked_at  INTEGER NOT NULL DEFAULT 0,
  certificate TEXT NOT NULL DEFAULT '',
  UNIQUE(domain, port, ip)
);

-- The watch list as watched_domains had it. This runs on every boot and
-- brings back nothing removed since, because removing an endpoint removes
-- its watched_domains row as well; watched_domains is otherwise left as it
-- was, for a downgrade to find.
INSERT OR IGNORE INTO watched_endpoints(domain, port, ip, created_at)
  SELECT domain, port, '', created_at FROM watched_domains;

-- Every TLS report, so the page opens on the last one and says what changed
-- since the one before. report is the scan as the page reads it; the other
-- columns are what the history's sparkline and list need without decoding
-- it. Kept to 100 per target and 180 days.
CREATE TABLE IF NOT EXISTS tls_scans (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  domain      TEXT NOT NULL,
  port        INTEGER NOT NULL,
  grade       TEXT NOT NULL DEFAULT '',
  days_left   INTEGER,
  fingerprint TEXT NOT NULL DEFAULT '',
  reachable   INTEGER NOT NULL DEFAULT 0,
  checked_at  INTEGER NOT NULL,
  report      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tls_scans_target ON tls_scans(domain, port, checked_at);

-- Every check of a watched endpoint, by the schedule or an administrator,
-- so a row can show how its days left moved and when its certificate
-- changed. Kept to 2000 per endpoint and 90 days.
CREATE TABLE IF NOT EXISTS watched_checks (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  endpoint_id INTEGER NOT NULL REFERENCES watched_endpoints(id) ON DELETE CASCADE,
  checked_at  INTEGER NOT NULL,
  days_left   INTEGER,
  fingerprint TEXT NOT NULL DEFAULT '',
  error       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_watched_checks_endpoint ON watched_checks(endpoint_id, checked_at);
`
