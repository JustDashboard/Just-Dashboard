package store

// githubPageSchema holds the GitHub page's repository App and the GitHub
// accounts each dashboard account connected through it
// (internal/githubrepos). Concatenated into `schema`, so the same rules hold:
// CREATE ... IF NOT EXISTS only, and a column one of these tables gains after
// it has shipped goes into addedColumns.
const githubPageSchema = `
-- The repository App, one per dashboard. It is apart from github_app (the
-- deploy App): that one acts as itself, this one acts as the person who
-- connected an account. secret_enc is one sealed JSON document holding the
-- client secret and the private key; everything else is what the page shows.
CREATE TABLE IF NOT EXISTS github_repo_app (
  id                 INTEGER PRIMARY KEY CHECK (id = 1),
  app_id             INTEGER NOT NULL,
  slug               TEXT NOT NULL,
  name               TEXT NOT NULL,
  owner              TEXT NOT NULL DEFAULT '',
  owner_type         TEXT NOT NULL DEFAULT 'User',
  html_url           TEXT NOT NULL DEFAULT '',
  client_id          TEXT NOT NULL,
  public             INTEGER NOT NULL DEFAULT 1,
  callback_urls_json TEXT NOT NULL DEFAULT '[]',
  permissions_json   TEXT NOT NULL DEFAULT '{}',
  secret_enc         TEXT NOT NULL,
  created_at         INTEGER NOT NULL
);
-- One GitHub account a dashboard account connected. It belongs to that
-- dashboard account alone and goes with it. tokens_enc is one sealed JSON
-- document holding the user access token and the refresh token; the two
-- expiries are kept beside it so a refresh can be decided without opening it.
-- status is active, expired or revoked.
CREATE TABLE IF NOT EXISTS github_connections (
  id                    INTEGER PRIMARY KEY AUTOINCREMENT,
  owner_user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  github_id             INTEGER NOT NULL,
  login                 TEXT NOT NULL,
  name                  TEXT NOT NULL DEFAULT '',
  html_url              TEXT NOT NULL DEFAULT '',
  method                TEXT NOT NULL DEFAULT 'web',
  status                TEXT NOT NULL DEFAULT 'active',
  status_message        TEXT NOT NULL DEFAULT '',
  tokens_enc            TEXT NOT NULL,
  access_expires_at     INTEGER NOT NULL DEFAULT 0,
  refresh_expires_at    INTEGER NOT NULL DEFAULT 0,
  installations_json    TEXT NOT NULL DEFAULT '[]',
  installations_read_at INTEGER NOT NULL DEFAULT 0,
  created_at            INTEGER NOT NULL,
  updated_at            INTEGER NOT NULL,
  UNIQUE(owner_user_id, github_id)
);
CREATE INDEX IF NOT EXISTS github_connections_github_id ON github_connections(github_id);
`
