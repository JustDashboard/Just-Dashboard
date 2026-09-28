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
`
