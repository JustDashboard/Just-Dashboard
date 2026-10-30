package store

// dnsRecordSchema holds the address records this dashboard wrote into an
// operator's zone for a deployment's domain (api/handlers_deploy_dns_record.go):
// one row a name and type, with the values and TTL as written, through which
// saved credential and into which zone, and who wrote it and when. It is how
// the Domains settings say "managed here, created then, by them", and how a
// dry run tells a record this dashboard wrote from one it did not: a record
// whose values still match the row is this dashboard's. Nothing reads a row
// to delete a record — none is ever deleted. Concatenated into `schema`:
// CREATE ... IF NOT EXISTS only, and a column added after it has shipped
// goes into addedColumns.
const dnsRecordSchema = `
CREATE TABLE IF NOT EXISTS deploy_dns_records (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  hostname       TEXT NOT NULL,
  record_type    TEXT NOT NULL,
  project_id     INTEGER NOT NULL DEFAULT 0,
  environment_id INTEGER NOT NULL DEFAULT 0,
  record_values  TEXT NOT NULL DEFAULT '[]',
  ttl            INTEGER NOT NULL DEFAULT 0,
  proxied        INTEGER NOT NULL DEFAULT 0,
  provider       TEXT NOT NULL DEFAULT '',
  zone           TEXT NOT NULL DEFAULT '',
  created_by     TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  updated_by     TEXT NOT NULL DEFAULT '',
  updated_at     INTEGER NOT NULL,
  UNIQUE(hostname, record_type)
);
CREATE INDEX IF NOT EXISTS idx_deploy_dns_records_environment ON deploy_dns_records(environment_id);
`
