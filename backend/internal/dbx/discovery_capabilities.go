package dbx

// What finding a database on this machine can lead to, per engine, for the
// driver catalogue.
func init() {
	RegisterCapabilities(
		// Can be connected from the inventory by its key.
		Capability{"inventoryConnect", everyEngine},
		// An account can be made or reset on a host server from here. The
		// forks named have no local sign-in the dashboard can use for it, and
		// SQL Server and Oracle have to be connected with a password they
		// already have.
		Capability{"hostAccount", On("postgres", "mysql", "mongodb", "redis", "clickhouse").
			Except(FlavorCockroachDB, FlavorYugabyteDB, FlavorTiDB, FlavorFerretDB, FlavorKeyDB, FlavorDragonfly)},
		// The "server" is a file: there is no host, port or account to ask for.
		Capability{"fileBased", On("sqlite")},
		// A host server of this kind is tried without a password by the sync.
		Capability{"openByDefault", On("mongodb", "redis", "clickhouse", FlavorTiDB).Except(FlavorFerretDB)},
	)
}
