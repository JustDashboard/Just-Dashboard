package store

import (
	"context"
	"database/sql"
)

// These tables are private administrator state. Native credentials remain
// sealed; reviewed requests contain only the closed DNS operation vocabulary.
const networkDNSServicesSchema = `
CREATE TABLE IF NOT EXISTS network_dns_service_settings (
 key TEXT PRIMARY KEY,
 value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS network_dns_services (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 engine TEXT NOT NULL,
 endpoint TEXT NOT NULL,
 server_name TEXT NOT NULL DEFAULT '',
 custom_ca INTEGER NOT NULL DEFAULT 0,
 secret_enc TEXT NOT NULL,
 management INTEGER NOT NULL DEFAULT 0,
 generation INTEGER NOT NULL DEFAULT 1,
 ownership TEXT NOT NULL DEFAULT 'connected',
 container_id TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS network_dns_service_changes (
 id TEXT PRIMARY KEY,
 connection_id TEXT NOT NULL REFERENCES network_dns_services(id) ON DELETE CASCADE,
 generation INTEGER NOT NULL,
 request_json TEXT NOT NULL,
 before_json TEXT NOT NULL,
 after_json TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL DEFAULT 'planned',
 created_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL,
 ended_at INTEGER NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS network_dns_service_changes_owner
 ON network_dns_service_changes(connection_id,state);
CREATE TABLE IF NOT EXISTS network_dns_service_provisions (
 id TEXT PRIMARY KEY,
 request_json TEXT NOT NULL,
 secret_enc TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'planned',
 resources_json TEXT NOT NULL DEFAULT '{}',
 connection_id TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL,
 ended_at INTEGER NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT ''
);
`

func InitializeNetworkDNSServices(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, networkDNSServicesSchema)
	return err
}
