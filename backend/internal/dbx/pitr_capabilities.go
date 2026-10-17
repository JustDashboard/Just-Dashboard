package dbx

// Point-in-time recovery, for the driver catalogue. PostgreSQL's write-ahead
// log is what WAL-G archives; CockroachDB and YugabyteDB speak its protocol
// and keep no such log. Whether one connection's server can be set up for it
// — a container this dashboard can restart with the archive mounted — is
// answered by GET /databases/{id}/pitr, since the engine alone does not say.
func init() {
	RegisterCapabilities(
		Capability{"pitr", On("postgres").Except(FlavorCockroachDB, FlavorYugabyteDB)},
	)
}
