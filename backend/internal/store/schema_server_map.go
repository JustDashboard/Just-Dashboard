package store

// serverMapSchema holds the server map's one table (api/handlers_map.go): how
// each account arranged its map — where it put the boxes it moved, which
// branches and shelves it folded, where it left the view. Kept apart from
// `schema` so the block is read in one place; it is concatenated into
// `schema`, so the same rules hold: CREATE ... IF NOT EXISTS only, and a
// column the table gains after it has shipped goes into addedColumns.
const serverMapSchema = `
-- One JSON document per account rather than a row per box, because the map
-- reads and writes it whole and nothing else reads it at all. The server only
-- checks it is a JSON object under a size cap; every field in it is a choice
-- about a picture, and the picture is the only thing that decodes it. It names
-- things the account can already list, so nothing in it is secret. It goes
-- with the account.
CREATE TABLE IF NOT EXISTS map_layouts (
  user_id    INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  layout     TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
`
