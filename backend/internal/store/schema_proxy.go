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
`
