package store

import (
	"context"
	"database/sql"
)

// InitializeNetworkDNSEvidence is additive for existing installations. Startup
// wiring calls it before the DNS adapter reconciles interrupted records.
func InitializeNetworkDNSEvidence(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, networkDNSEvidenceSchema)
	return err
}

const networkDNSEvidenceSchema = `
CREATE TABLE IF NOT EXISTS network_dns_evidence (
  id TEXT PRIMARY KEY,
  request_json TEXT NOT NULL,
  actor TEXT NOT NULL DEFAULT '',
  started_at INTEGER NOT NULL,
  ended_at INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'running',
  artifact_json TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_network_dns_evidence_at ON network_dns_evidence(started_at DESC);
CREATE TRIGGER IF NOT EXISTS network_dns_evidence_scope_immutable
BEFORE UPDATE OF id,request_json,actor,started_at ON network_dns_evidence
BEGIN SELECT RAISE(ABORT, 'DNS evidence scope is immutable'); END;
`
