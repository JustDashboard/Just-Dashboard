package dbx

// What a connection's server holds beside the database the connection opens,
// per engine, for the driver catalogue: whether another database can be made
// there, and whether one that is there can be opened as a connection of its
// own. The two routes ask the functions below before they do anything, so a
// flag and the route it describes cannot come to disagree.

// CanCreateDatabase reports whether a database can be made on the server a
// connection of this engine opens. The SQL engines that can are the ones with
// a server surface to make it through; MongoDB makes one by putting a
// collection in it. Redis numbers its keyspaces itself, a SQLite database is
// its file, and Oracle's is the instance.
func CanCreateDatabase(d Driver) bool {
	if d == DriverMongo {
		return true
	}
	_, err := AdminFor(d)
	return err == nil
}

// CanConnectSibling reports whether another database of the same server can
// be saved as a connection of its own, under the credentials this one has:
// whether the engine's connection string can be pointed at a database by
// name. ClickHouse's is left where the operator's grants are and every
// statement names its database instead; Redis has numbers, chosen per request
// rather than per connection; SQLite and Oracle have one database per
// connection.
func CanConnectSibling(d Driver) bool {
	switch d {
	case DriverPostgres, DriverMySQL, DriverMSSQL, DriverMongo:
		return true
	}
	return false
}

func init() {
	RegisterCapabilities(
		// POST /server/databases makes a database on the connection's server.
		Capability{"serverDatabaseCreate", func(d Driver, _ string) any { return CanCreateDatabase(d) }},
		// POST /server/databases/connect opens another database of the same
		// server as a connection of its own, and the create route's `connect`
		// does so for the database it has just made.
		Capability{"serverDatabaseConnect", func(d Driver, _ string) any { return CanConnectSibling(d) }},
	)
}
