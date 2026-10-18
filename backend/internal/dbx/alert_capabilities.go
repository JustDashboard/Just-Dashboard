package dbx

// What a database can be watched for, per engine, for the driver catalogue.
// The alert routes ask AlertKinds before they save a rule for one database,
// and the evaluator before it judges one, so a kind the page offers and a
// kind the server will judge cannot come to disagree.

// The kinds of database alert.
const (
	// AlertDown: the server has not answered for the rule's minutes.
	AlertDown = "down"
	// AlertDisk: the filesystem the data is on is fuller than the line.
	AlertDisk = "disk"
	// AlertConnections: the sessions held are near the server's own limit.
	AlertConnections = "connections"
	// AlertReplication: a replica is further behind than the line, or has
	// stopped following.
	AlertReplication = "replication"
	// AlertBackupFailed: the last run of a backup job that dumps the
	// database failed.
	AlertBackupFailed = "backup_failed"
	// AlertBackupMissed: a scheduled job has had no successful run since the
	// time it was due, and the grace after it.
	AlertBackupMissed = "backup_missed"
)

// AlertKindOrder is every kind, in the order a form offers them.
var AlertKindOrder = []string{AlertDown, AlertDisk, AlertConnections, AlertReplication, AlertBackupFailed, AlertBackupMissed}

// connectionLimitEngines report the sessions they hold against a limit they
// were configured with: the SQL engines with a statistics snapshot,
// Redis's maxclients and MongoDB's available connections. FerretDB's
// serverStatus has no connection counts.
var connectionLimitEngines = anyOf(
	func(d Driver, flavor string) any { return d != DriverSQLite && OpsCapabilities(d, flavor)["stats"] },
	redisEngines, mongoDBProper,
)

// AlertKinds is what a database of this engine can be watched for. A server
// can be down, fill its disk, run out of connections and fall behind; a
// SQLite file is none of those, and like every engine it can be in a backup
// job that fails or is missed.
func AlertKinds(d Driver, flavor string) []string {
	out := []string{}
	if !d.Valid() {
		return out
	}
	if d != DriverSQLite {
		out = append(out, AlertDown, AlertDisk)
		if on, _ := connectionLimitEngines(d, flavor).(bool); on {
			out = append(out, AlertConnections)
		}
		if on, _ := replicationEngines(d, flavor).(bool); on {
			out = append(out, AlertReplication)
		}
	}
	return append(out, AlertBackupFailed, AlertBackupMissed)
}

// AlertKindKnown reports whether kind is one of the kinds at all.
func AlertKindKnown(kind string) bool {
	for _, known := range AlertKindOrder {
		if kind == known {
			return true
		}
	}
	return false
}

func init() {
	RegisterCapabilities(
		// GET/POST /databases/alerts: what a rule for one of these databases
		// may watch for. Empty for an engine there is no driver for.
		Capability{"alertKinds", func(d Driver, flavor string) any { return AlertKinds(d, flavor) }},
	)
}
