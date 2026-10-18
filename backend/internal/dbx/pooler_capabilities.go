package dbx

// Whether a connection pooler can be put in front of the engine. PgBouncer
// speaks PostgreSQL's wire protocol and is run beside a server the dashboard
// can see as a container; CockroachDB and YugabyteDB answer the protocol with
// connection handling of their own and are not pooled this way.
func init() {
	RegisterCapabilities(
		Capability{"pooler", On("postgres").Except(FlavorCockroachDB, FlavorYugabyteDB)},
	)
}
