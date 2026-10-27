package store

// passkeySchema holds the dashboard's WebAuthn passkeys (auth/passkeys.go):
// discoverable credentials that stand in for an account's password at
// sign-in, and the ceremonies already finished. Kept apart from `schema` so the block
// is read in one place; it is concatenated into `schema`, so the same rules
// hold: CREATE ... IF NOT EXISTS only, and a column added after it has
// shipped goes into addedColumns.
const passkeySchema = `
-- A credential: its id (base64url, unique across accounts), the library's
-- full record of it as JSON (public key, flags, signature counter, AAGUID),
-- the relying party ID it was registered under — the host name it signs in
-- at — and the name its owner gave it. It goes with the account.
CREATE TABLE IF NOT EXISTS webauthn_credentials (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  credential_id   TEXT NOT NULL UNIQUE,
  credential_json TEXT NOT NULL,
  rp_id           TEXT NOT NULL,
  name            TEXT NOT NULL,
  created_at      INTEGER NOT NULL,
  last_used_at    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_webauthn_credentials_user ON webauthn_credentials(user_id);

-- A ceremony that has been finished, by the id sealed inside its challenge,
-- until the challenge would have expired anyway: a response is tried against
-- a challenge once. Ceremonies in flight are not kept here; their challenge is
-- sealed and held by the browser.
CREATE TABLE IF NOT EXISTS webauthn_spent_challenges (
  id         TEXT PRIMARY KEY,
  expires_at INTEGER NOT NULL
);
`
