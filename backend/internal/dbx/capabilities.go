package dbx

import "sync"

// What each engine can be asked for, stated once.
//
// The driver catalogue used to carry two booleans — is it SQL, does the DDL
// builder work — and the page worked out the rest by comparing a connection's
// driver against strings of its own: which engines have roles, which keep
// statement statistics, which can explain a plan. Every such comparison is a
// second copy of a fact this package already enforces, and the copies drifted
// the moment an engine gained a feature. A tab drawn for an engine whose route
// answers 400 is the visible form of that drift.
//
// So the answer is a table keyed by feature, and the catalogue hands the page
// the whole reading per engine and per flavour. A flag is a statement about
// the product — "this engine has roles, and this dashboard can manage them" —
// not about one server on one day: a route that still answers
// `supported: false` for a particular server (statement statistics on a
// Postgres without the extension, say) is that server's condition, reported
// by the route, and the flag stays true because the section belongs on the
// page.

// CapabilityRule decides one flag for one engine. The value is a boolean for
// nearly every flag; a rule may return a string where the page needs a word
// rather than a yes.
type CapabilityRule func(d Driver, flavor string) any

// Capability is one row of the table: the flag the frontend gates on and who
// has it.
type Capability struct {
	Flag string
	Has  CapabilityRule
}

// On grants a flag to the engines named. A driver id grants it to every
// flavour of that driver; a flavour id that is not a driver's own grants it to
// that flavour alone.
func On(names ...string) CapabilityRule {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return func(d Driver, flavor string) any {
		return set[string(d)] || set[flavor]
	}
}

// Except takes a flag away again from the flavours named, for a fork that
// speaks the driver's protocol and lacks the feature.
func (rule CapabilityRule) Except(flavorIDs ...string) CapabilityRule {
	return func(d Driver, flavor string) any {
		for _, f := range flavorIDs {
			if f == flavor {
				return false
			}
		}
		return rule(d, flavor)
	}
}

// sqlEngines grants a flag to everything that goes through the dialect layer.
func sqlEngines(d Driver, _ string) any { return d.IsSQL() }

// everyEngine grants a flag to every engine there is a driver for.
func everyEngine(d Driver, _ string) any { return d.Valid() }

// anyOf grants a flag where any of the rules does. It is how a flag several
// engine families share is stated once: the SQL dialects answer from the
// interfaces they implement, and Redis and MongoDB are named beside them.
func anyOf(rules ...CapabilityRule) CapabilityRule {
	return func(d Driver, flavor string) any {
		for _, rule := range rules {
			if on, _ := rule(d, flavor).(bool); on {
				return true
			}
		}
		return false
	}
}

// opsFlag reads a flag from what the SQL dialects implement. It answers false
// for an engine with no dialect, so a row built on it names Redis and MongoDB
// itself where they have the feature.
func opsFlag(flag string) CapabilityRule {
	return func(d Driver, flavor string) any { return OpsCapabilities(d, flavor)[flag] }
}

// Redis and MongoDB, for the rows below. FerretDB speaks MongoDB's protocol
// over another engine and lacks what is the server's own: its sessions, its
// replication, its views and its plans.
var (
	redisEngines  = On(string(DriverRedis))
	mongoEngines  = On(string(DriverMongo))
	mongoDBProper = On(string(DriverMongo)).Except(FlavorFerretDB)
)

// capabilityTable is the reading the driver catalogue serves. It holds the
// flags more than one surface answers for; a flag that belongs to one surface
// is kept beside that surface's code and handed to RegisterCapabilities from
// an init (the *_capabilities.go files). Every flag is stated exactly once,
// in one place or the other: a second row of the same name would decide the
// flag for every engine, not only for the one its author had in mind.
var capabilityTable = []Capability{
	// A statement runner over database/sql, and the forms built on it.
	{"sql", sqlEngines},
	{"advisor", sqlEngines},
	{"orm", sqlEngines},
	// Something to type a statement or a command into: the SQL editor, the
	// Redis console, MongoDB's command runner.
	{"console", everyEngine},
	{"ddl", func(d Driver, _ string) any {
		dialect, err := DialectFor(d)
		return err == nil && dialect.SupportsDDL()
	}},
	// The `schema` parameter selects among several namespaces. SQLite has the
	// one, "main", and nothing to choose.
	{"schemas", On("postgres", "mysql", "sqlserver", "clickhouse", "oracle")},
	// Views are listed with their type and can be defined. FerretDB has none.
	{"views", anyOf(sqlEngines, mongoDBProper)},
	{"indexes", anyOf(sqlEngines, mongoEngines)},
	// The catalogue is listed for both; only Postgres can change it, which
	// the extensions route says with `editable`. CockroachDB and TiDB accept
	// the catalogue query and have nothing to put in it.
	{"extensions", On("postgres", "mysql").Except(FlavorCockroachDB, FlavorTiDB)},
	{"roles", On("postgres", "mysql", "sqlserver", "clickhouse", "mongodb", "redis")},
	// One snapshot of counters and gauges for charts.
	{"stats", anyOf(opsFlag("stats"), redisEngines, mongoEngines)},
	// Who is connected and what they are running: sessions, Redis clients,
	// MongoDB's current operations.
	{"sessions", anyOf(opsFlag("sessions"), redisEngines, mongoDBProper)},
	// End a session, and stop what one is running while keeping it. Redis
	// disconnects a client and cannot stop one command; MongoDB stops an
	// operation and has no session to end.
	{"kill", anyOf(opsFlag("kill"), redisEngines)},
	{"cancel", anyOf(opsFlag("cancel"), mongoDBProper)},
	// The server's parameters, and changing one where the engine keeps it.
	{"settings", everyEngine},
	{"settingsWrite", anyOf(opsFlag("settingsWrite"), redisEngines)},
	{"replication", anyOf(opsFlag("replication"), redisEngines, mongoDBProper)},
	// A plan as a tree, and a plan measured by running the statement.
	{"explainJSON", anyOf(explainForm(false), mongoDBProper)},
	{"explainAnalyze", anyOf(explainForm(true), mongoDBProper)},
	{"queryLog", On("postgres", "mysql", "clickhouse", "mongodb", "redis")},
	{"dump", everyEngine},
	// A table or a collection as a file. Redis has neither.
	{"export", anyOf(sqlEngines, mongoEngines)},
	// A server process, as opposed to a file: something that can be started
	// and stopped, that listens somewhere and writes a log.
	{"server", func(d Driver, _ string) any { return d.Valid() && d != DriverSQLite }},
	// The dashboard can start one from its own closed list of images.
	{"provision", On("postgres", "mysql", "redis", "mongodb", "clickhouse").
		Except(FlavorCockroachDB, FlavorYugabyteDB, FlavorPercona, FlavorTiDB, FlavorFerretDB)},
}

// listOf is the answer of a flag whose value is a list. Never nil: an engine
// with nothing to offer reads `[]`, which a page can take the length of.
func listOf[T ~string](items []T) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = string(item)
	}
	return out
}

// explainForm reads the two plan forms beyond the plain text one from the
// dialect that renders them. CockroachDB and TiDB speak their driver's wire
// and print plans of their own, which the plan tree does not read.
func explainForm(analyze bool) CapabilityRule {
	return func(d Driver, flavor string) any {
		jsonPlan, measured := ExplainForms(d)
		switch {
		case flavor == FlavorCockroachDB:
			return false
		case analyze:
			return measured
		}
		return jsonPlan && flavor != FlavorTiDB
	}
}

var (
	capabilityMu    sync.RWMutex
	capabilityExtra []Capability
)

// RegisterCapabilities adds rows from the file that owns the feature they
// describe, so a new engine surface states what it supports beside the code
// that supports it. A flag registered here replaces a row of the same name —
// for every engine, which is never what a file about one engine means to do,
// so TestCapabilityTableIsCoherent refuses a flag that is stated twice.
func RegisterCapabilities(rows ...Capability) {
	capabilityMu.Lock()
	defer capabilityMu.Unlock()
	capabilityExtra = append(capabilityExtra, rows...)
}

// Capabilities is everything the table says about one engine. An empty
// flavour reads as the driver's own.
func Capabilities(d Driver, flavor string) map[string]any {
	if flavor == "" || !FlavorOf(d, flavor) {
		flavor = DefaultFlavor(d)
	}
	capabilityMu.RLock()
	defer capabilityMu.RUnlock()
	out := make(map[string]any, len(capabilityTable)+len(capabilityExtra))
	for _, row := range capabilityTable {
		out[row.Flag] = row.Has(d, flavor)
	}
	for _, row := range capabilityExtra {
		out[row.Flag] = row.Has(d, flavor)
	}
	return out
}

// Capable reports one boolean flag, false for a flag nobody declared and for
// one whose value is a word.
func Capable(d Driver, flavor, flag string) bool {
	on, _ := Capabilities(d, flavor)[flag].(bool)
	return on
}
