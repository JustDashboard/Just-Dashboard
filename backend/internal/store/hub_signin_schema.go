package store

// Hub sign-in (hub-link-protocol.md section 9): each account's permission for
// one hub user to sign it in, and the nonce of every ticket redeemed, so a
// ticket opens at most one session. Both are cleared with the hub link. The
// session a ticket makes is an ordinary row in sessions, marked by
// auth_method 'hub' and the hub user it was made for (sessions.hub_user,
// added through addedColumns).
const hubSignInSchema = `
CREATE TABLE IF NOT EXISTS hub_signin_grants (
  user_id    INTEGER PRIMARY KEY,
  hub_user   TEXT NOT NULL UNIQUE,
  server_id  TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS hub_signin_tickets (
  nonce      TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL,
  used_at    INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_hub_signin_tickets_expires ON hub_signin_tickets(expires_at);
`
