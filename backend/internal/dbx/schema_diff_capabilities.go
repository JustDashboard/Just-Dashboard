package dbx

// Which engines can compare two schemas and write the migration between them.
// The products that speak a driver's protocol with a DDL of their own are left
// out: TimescaleDB turns a table into a hypertable with a call a schema read
// cannot see, CockroachDB, YugabyteDB and TiDB differ from their driver in the
// statements this writes, and Azure SQL Edge lacks parts of SQL Server's.
func init() {
	RegisterCapabilities(Capability{"schemaDiff",
		On(string(DriverPostgres), string(DriverMySQL), string(DriverMSSQL), string(DriverSQLite)).
			Except(FlavorTimescaleDB, FlavorCockroachDB, FlavorYugabyteDB, FlavorTiDB, FlavorAzureSQLEdge)})
}
