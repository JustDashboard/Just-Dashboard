package dbx

// Row-level security is PostgreSQL's, and so are its forks that kept it:
// TimescaleDB and YugabyteDB answer pg_policy. CockroachDB speaks the wire
// protocol and has no such catalogue to read, as it has no aclexplode for the
// grants beside it.
func init() {
	RegisterCapabilities(
		// GET /rls, the /ddl/policy and /ddl/row-security forms, and POST /rls/test.
		Capability{"rowSecurity", On(string(DriverPostgres)).Except(FlavorCockroachDB)},
	)
}
