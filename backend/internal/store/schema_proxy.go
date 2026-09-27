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
`
