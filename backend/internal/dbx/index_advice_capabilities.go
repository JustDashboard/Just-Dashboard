package dbx

// What index advice on request is offered for. Both are tools the operator
// runs from the Explain view; neither is a finding.
func init() {
	RegisterCapabilities(
		// A hypothetical index tried on a plan with HypoPG. The flag is the
		// product's: whether the extension is in a given database is the
		// route's answer. CockroachDB and YugabyteDB are PostgreSQL on the
		// wire and have no HypoPG.
		Capability{"indexWhatIf", On("postgres").Except(FlavorCockroachDB, FlavorYugabyteDB)},
		// The missing-index views SQL Server keeps about its own plans.
		Capability{"missingIndexes", On("sqlserver")},
	)
}
