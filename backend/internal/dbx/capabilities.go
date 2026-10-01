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

// nobody is a feature no route serves yet. The row is kept so the flag is
// always present in the reading: a page gating on an absent key cannot tell
// "this engine lacks it" from "this build does not know the word".
func nobody(Driver, string) any { return false }

// capabilityTable is the reading the driver catalogue serves. Adding a flag,
// or giving an engine one it lacked, is one line here; a file that owns a
// feature may instead keep its rows beside its code and hand them to
// RegisterCapabilities from an init.
var capabilityTable = []Capability{
	// A statement runner over database/sql, and the forms built on it.
	{"sql", sqlEngines},
	{"console", sqlEngines},
	{"orm", sqlEngines},
	{"advisor", sqlEngines},
	{"ddl", func(d Driver, _ string) any {
		dialect, err := DialectFor(d)
		return err == nil && dialect.SupportsDDL()
	}},
	// The `schema` parameter selects among several namespaces. SQLite has the
	// one, "main", and nothing to choose.
	{"schemas", On("postgres", "mysql", "sqlserver", "clickhouse", "oracle")},
	// Views are listed among the tables with their type. Nothing returns a
	// routine, a trigger, a sequence or an enumerated type yet.
	{"views", sqlEngines},
	{"routines", nobody},
	{"triggers", nobody},
	{"sequences", nobody},
	{"enums", nobody},
	// The catalogue is listed for both; only Postgres can change it, which
	// the extensions route says with `editable`. CockroachDB and TiDB accept
	// the catalogue query and have nothing to put in it.
	{"extensions", On("postgres", "mysql").Except(FlavorCockroachDB, FlavorTiDB)},
	{"roles", On("postgres", "mysql", "sqlserver", "clickhouse", "mongodb", "redis")},
	{"sessions", On("postgres", "mysql", "sqlserver", "clickhouse", "oracle")},
	{"cancel", nobody},
	{"locks", nobody},
	// pg_stat_statements and the performance schema's digest table.
	// CockroachDB has neither the extension nor a way to install it.
	{"statements", On("postgres", "mysql").Except(FlavorCockroachDB)},
	{"maintenance", nobody},
	// Read only: no route changes a server parameter. Mongo and Redis answer
	// with their status flattened into the same list. KeyDB is Redis 6
	// underneath and refuses the INFO that reading takes, which asks for
	// several sections at once.
	{"settings", On("postgres", "mysql", "sqlserver", "clickhouse", "mongodb", "redis").Except(FlavorKeyDB)},
	{"replication", nobody},
	{"explainJSON", nobody},
	{"explainAnalyze", nobody},
	{"changeSets", nobody},
	{"transactions", nobody},
	{"queryLog", On("postgres", "mysql", "clickhouse", "mongodb", "redis")},
	{"dump", func(d Driver, _ string) any { return d.Valid() }},
	// A server process, as opposed to a file: something that can be started
	// and stopped, that listens somewhere and writes a log.
	{"server", func(d Driver, _ string) any { return d.Valid() && d != DriverSQLite }},
	// The dashboard can start one from its own closed list of images.
	{"provision", On("postgres", "mysql", "redis", "mongodb", "clickhouse").
		Except(FlavorCockroachDB, FlavorYugabyteDB, FlavorPercona, FlavorTiDB, FlavorFerretDB)},
}

var (
	capabilityMu    sync.RWMutex
	capabilityExtra []Capability
)

// RegisterCapabilities adds rows from the file that owns the feature they
// describe, so a new engine surface states what it supports beside the code
// that supports it. A flag registered here replaces a row of the same name in
// the table above: the file that serves the route is the one that knows.
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
	out := make(map[string]any, len(capabilityTable))
	for _, row := range capabilityTable {
		out[row.Flag] = row.Has(d, flavor)
	}
	capabilityMu.RLock()
	defer capabilityMu.RUnlock()
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
