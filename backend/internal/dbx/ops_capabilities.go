package dbx

// What watching and maintaining a SQL server offers per engine, read from the
// interfaces each dialect implements (OpsCapabilities), for the driver
// catalogue. Only the flags no other engine family has are registered here.
// The ones Redis and MongoDB share — stats, sessions, kill, cancel, settings,
// settingsWrite, replication, roles — are rows of the table in
// capabilities.go, which read the same function for the SQL engines.
func init() {
	for _, flag := range []string{
		"locks", "tableStats", "indexStats", "maintenance", "privileges", "statements", "statementsReset",
		"engineAdvisor", "clickhouseViews", "sqliteFile",
	} {
		RegisterCapabilities(Capability{Flag: flag, Has: opsFlag(flag)})
	}
	RegisterCapabilities(
		// The closed set of maintenance actions the engine offers, by id.
		Capability{"maintenanceActions", func(d Driver, flavor string) any {
			ids := []string{}
			if !OpsCapabilities(d, flavor)["maintenance"] {
				return ids
			}
			for _, action := range MaintenanceActionsFor(d) {
				ids = append(ids, action.ID)
			}
			return ids
		}},
	)
}
