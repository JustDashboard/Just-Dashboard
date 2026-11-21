package store

// fileVersionSchema holds what the Files page keeps of a file's earlier
// contents (internal/fileversions). The contents themselves are files under
// the data directory, named by their SHA-256; these tables are what points at
// them. Concatenated into `schema`, so the same rules hold: CREATE ... IF NOT
// EXISTS only, and a column one of these tables gains after it has shipped
// goes into addedColumns.
const fileVersionSchema = `
-- One earlier content of a file. path is the resolved path the file had
-- when it was replaced (a move through the dashboard carries it along).
-- revision is the SHA-256 of the content, and the name of the kept copy.
-- at and by say when the content was replaced and by whom, except for a
-- 'displaced' row, the dashboard's own save that something outside the
-- dashboard replaced later, where they say when and by whom it was saved.
-- reason is how the dashboard replaced it: 'save', 'image', 'restore',
-- 'displaced'. outside is 1 when the content replaced was not what the
-- dashboard last wrote there: it was changed outside the dashboard.
CREATE TABLE IF NOT EXISTS file_versions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  path       TEXT NOT NULL,
  revision   TEXT NOT NULL,
  size       INTEGER NOT NULL DEFAULT 0,
  at         INTEGER NOT NULL,
  by         TEXT NOT NULL DEFAULT '',
  reason     TEXT NOT NULL DEFAULT 'save',
  outside    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS file_versions_path ON file_versions(path, id);
CREATE INDEX IF NOT EXISTS file_versions_revision ON file_versions(revision);
-- What the dashboard last put at a path, so the next save can tell an edit
-- made in a shell in between. kept is 1 while that content's copy is held
-- (it becomes a 'displaced' version when it is found replaced from outside).
CREATE TABLE IF NOT EXISTS file_version_heads (
  path       TEXT PRIMARY KEY,
  revision   TEXT NOT NULL,
  size       INTEGER NOT NULL DEFAULT 0,
  at         INTEGER NOT NULL,
  by         TEXT NOT NULL DEFAULT '',
  kept       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS file_version_heads_revision ON file_version_heads(revision);
`
