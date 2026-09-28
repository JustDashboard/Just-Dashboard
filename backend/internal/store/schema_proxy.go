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

-- --- lane B: sites list & lifecycle ---

-- --- lane C: site builder ---

-- --- lane D: streams ---

-- --- lane E: ports & exposure ---

-- --- lane F: certificates ---

-- --- lane G: TLS report & monitoring ---
`
