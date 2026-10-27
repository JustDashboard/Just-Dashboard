package store

// secretStoreSchema holds the connections to external secret managers
// (internal/secretstore) that deployment variables reference as
// ${{store.NAME/path#KEY}}. Kept apart from `schema` so the block is read in
// one place; it is concatenated into `schema`, so the same rules hold:
// CREATE ... IF NOT EXISTS only, and a column this table gains after it has
// shipped goes into addedColumns.
const secretStoreSchema = `
-- A connection: the store's address and non-secret settings in the clear, its
-- credentials sealed in credentials_enc and read back by name only. Values
-- read from a store are never written here; tested_* and read_* hold what the
-- last Test and the last run's read answered, without a value.
CREATE TABLE IF NOT EXISTS deploy_secret_stores (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  name            TEXT NOT NULL UNIQUE,
  kind            TEXT NOT NULL,
  address         TEXT NOT NULL,
  settings_json   TEXT NOT NULL DEFAULT '{}',
  credentials_enc TEXT NOT NULL DEFAULT '',
  allow_previews  INTEGER NOT NULL DEFAULT 0,
  created_by      TEXT NOT NULL DEFAULT '',
  updated_by      TEXT NOT NULL DEFAULT '',
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL,
  tested_at       INTEGER NOT NULL DEFAULT 0,
  test_ok         INTEGER NOT NULL DEFAULT 0,
  test_message    TEXT NOT NULL DEFAULT '',
  read_at         INTEGER NOT NULL DEFAULT 0,
  read_ok         INTEGER NOT NULL DEFAULT 0,
  read_message    TEXT NOT NULL DEFAULT ''
);
`
