package dbx

// What a major-version upgrade offers per engine, for the driver catalogue.
func init() {
	RegisterCapabilities(
		// POST /upgrade: a server the dashboard started is moved to a newer
		// major release of its own image. PostgreSQL only, and not its
		// forks: CockroachDB and YugabyteDB are not run from those images,
		// and TimescaleDB keeps its hypertables in a catalogue of its own
		// that a dump of one release does not carry into the next.
		Capability{"majorUpgrade", On(string(DriverPostgres)).
			Except(FlavorCockroachDB, FlavorYugabyteDB, FlavorTimescaleDB)},
	)
}
