package store

// cloudflareTunnelSchema holds the Cloudflare Tunnel connection
// (cftunnel/manager.go): the one tunnel this dashboard runs a connector for,
// how it was connected (a pasted token, or created through the API with the
// credentials saved under Certificates), and its token sealed with the
// dashboard's key. applied_digest names the ingress rules last set at
// Cloudflare, so a pass that changes nothing sends nothing. Concatenated
// into `schema`: CREATE ... IF NOT EXISTS only, and a column added after it
// has shipped goes into addedColumns.
const cloudflareTunnelSchema = `
CREATE TABLE IF NOT EXISTS proxy_cloudflare_tunnel (
  id             INTEGER PRIMARY KEY CHECK (id = 1),
  mode           TEXT NOT NULL,
  tunnel_id      TEXT NOT NULL,
  tunnel_name    TEXT NOT NULL DEFAULT '',
  account_id     TEXT NOT NULL DEFAULT '',
  token_enc      TEXT NOT NULL,
  applied_digest TEXT NOT NULL DEFAULT '',
  created_by     TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);
`
