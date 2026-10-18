package dbx

// What the SQL editor's assistant (internal/assistant) is offered for. It
// drafts statements from a schema read through the dialects, so it is every
// engine with one; MongoDB and Redis have consoles of their own vocabulary
// and no editor for it to write into.
//
// Row-level security is PostgreSQL's and the forks that carry it whole.
// CockroachDB has a policy syntax of its own only in its newest releases, and
// a policy drafted for PostgreSQL is not one it can be trusted to run.
func init() {
	RegisterCapabilities(
		Capability{"assistant", sqlEngines},
		Capability{"assistantPolicy", On(string(DriverPostgres)).Except(FlavorCockroachDB)},
	)
}
